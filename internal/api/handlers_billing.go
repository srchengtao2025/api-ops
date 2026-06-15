// Vendor / Channel-Vendor handlers (Pricing 已下线 2026-06-14)
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/api-ops/api-ops/internal/dal"
	"github.com/api-ops/api-ops/internal/discount"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ===== Vendor =====

func (s *Server) listVendors(c *gin.Context) {
	rows, err := dal.ListVendors(c.Request.Context())
	if err != nil {
		errResp(c, 500, "list vendors failed", err.Error())
		return
	}
	ok(c, rows)
}

func (s *Server) createVendor(c *gin.Context) {
	var v dal.UpstreamVendor
	if err := c.ShouldBindJSON(&v); err != nil {
		errResp(c, 400, "invalid body", err.Error())
		return
	}
	if v.Code == "" || v.Name == "" {
		errResp(c, 400, "code 和 name 必填", nil)
		return
	}
	if err := dal.CreateVendor(c.Request.Context(), &v); err != nil {
		errResp(c, 500, "create vendor failed", err.Error())
		return
	}
	ok(c, v)
}

func (s *Server) updateVendor(c *gin.Context) {
	id := parseUint(c.Param("id"))
	var v dal.UpstreamVendor
	if err := c.ShouldBindJSON(&v); err != nil {
		errResp(c, 400, "invalid body", err.Error())
		return
	}
	v.ID = id
	if err := dal.UpdateVendor(c.Request.Context(), &v); err != nil {
		errResp(c, 500, "update vendor failed", err.Error())
		return
	}
	ok(c, v)
}

func (s *Server) deleteVendor(c *gin.Context) {
	id := parseUint(c.Param("id"))
	if err := dal.DeleteVendor(c.Request.Context(), id); err != nil {
		errResp(c, 500, "delete vendor failed", err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}

func (s *Server) listVendorChannels(c *gin.Context) {
	code := c.Param("code")
	mappings, err := dal.ListChannelVendors(c.Request.Context(), 0)
	if err != nil {
		errResp(c, 500, "list mappings failed", err.Error())
		return
	}
	var result []gin.H
	for _, m := range mappings {
		if m.VendorCode != code {
			continue
		}
		ch, _ := dal.GetChannel(c.Request.Context(), m.ChannelID)
		name := ""
		if ch != nil {
			name = ch.Name
		}
		result = append(result, gin.H{
			"channel_id":   m.ChannelID,
			"channel_name": name,
			"vendor_code":  m.VendorCode,
			"discount":     m.Discount,
		})
	}
	ok(c, result)
}

// ===== UpstreamPricing 已下线 (2026-06-14) =====
// v3 PR #2 之后, cost 反推改用 channel_vendor_map.discount, 价目表 0 引用
// 4 个 handler (listPricing/deletePricing/importPricing/getImport) 全删
// 表移到 archive schema (migrations/2026-06-14-upstream-pricing-archive.sql)

// ===== ChannelVendorMap =====

func (s *Server) listChannelVendors(c *gin.Context) {
	chID := parseInt(c.Query("channel_id"))
	rows, err := dal.ListChannelVendors(c.Request.Context(), chID)
	if err != nil {
		errResp(c, 500, "list failed", err.Error())
		return
	}
	ok(c, rows)
}

func (s *Server) upsertChannelVendor(c *gin.Context) {
	var m dal.ChannelVendorMap
	if err := c.ShouldBindJSON(&m); err != nil {
		errResp(c, 400, "invalid body", err.Error())
		return
	}
	if m.ChannelID == 0 || m.VendorCode == "" {
		errResp(c, 400, "channel_id 和 vendor_code 必填", nil)
		return
	}
	if m.Discount <= 0 {
		m.Discount = 1.0
	}
	if err := dal.UpsertChannelVendor(c.Request.Context(), &m); err != nil {
		errResp(c, 500, "upsert failed", err.Error())
		return
	}
	ok(c, m)
}

func (s *Server) deleteChannelVendor(c *gin.Context) {
	id := parseUint(c.Param("id"))
	if err := dal.DeleteChannelVendor(c.Request.Context(), id); err != nil {
		errResp(c, 500, "delete failed", err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}

// ===== A 阶段: 渠道供应商映射 (auto + final discount 矫正) =====

// ChannelMappingView 单条渠道的供应商 + 折扣视图
type ChannelMappingView struct {
	ChannelID      int     `json:"channel_id"`
	ChannelName    string  `json:"channel_name"`
	ChannelType    int     `json:"channel_type"`
	ChannelStatus  int     `json:"channel_status"`
	ChannelGroup   string  `json:"channel_group"`
	ChannelBalance float64 `json:"channel_balance"`
	// 已映射的供应商 (可能为空 = 未归类)
	MappingID  uint64 `json:"mapping_id"` // 0 = 未映射
	VendorCode string `json:"vendor_code"`
	VendorName string `json:"vendor_name"` // 来自 upstream_vendors.name
	// 折扣三段
	Discount         float64 `json:"discount"`          // final (矫正后 / 自动)
	AutoDiscount     float64 `json:"auto_discount"`     // 自动解析
	AutoMatched      string  `json:"auto_matched"`      // 解析匹配字符串
	AutoRecognized   bool    `json:"auto_recognized"`   // 解析是否成功
	DiscountOverride bool    `json:"discount_override"` // 是否人工矫正
	Remark           string  `json:"remark"`
}

// listChannelMappings GET /api/channel-mappings
//
// 自动:
//  1. 从 upstream 49 渠道全量拉
//  2. 对每条渠道名跑 ParseDiscountFromName (auto discount)
//  3. 跟 channel_vendor_map LEFT JOIN, 取 vendor_code + final discount
//  4. 返回结构化数组, SPA 一屏展示
func (s *Server) listChannelMappings(c *gin.Context) {
	ctx := c.Request.Context()
	// 1) 拉 49 渠道
	chs, err := dal.ListChannels(ctx, 0)
	if err != nil {
		errResp(c, 500, "list channels failed", err.Error())
		return
	}
	// 2) 拉所有 mapping
	maps, err := dal.ListChannelVendors(ctx, 0)
	if err != nil {
		errResp(c, 500, "list mappings failed", err.Error())
		return
	}
	// 3) 建索引: channel_id → mapping
	mapByCh := make(map[int]dal.ChannelVendorMap, len(maps))
	for _, m := range maps {
		mapByCh[m.ChannelID] = m
	}
	// 4) 拉所有 vendor (建索引 code → name)
	vds, err := dal.ListVendors(ctx)
	if err != nil {
		errResp(c, 500, "list vendors failed", err.Error())
		return
	}
	vdByCode := make(map[string]string, len(vds))
	for _, v := range vds {
		vdByCode[v.Code] = v.Name
	}

	// 5) 拼装
	views := make([]ChannelMappingView, 0, len(chs))
	for _, ch := range chs {
		auto := discount.ParseDiscountFromName(ch.Name)
		view := ChannelMappingView{
			ChannelID:      int(ch.ID),
			ChannelName:    ch.Name,
			ChannelType:    ch.Type,
			ChannelStatus:  ch.Status,
			ChannelGroup:   ch.Group,
			ChannelBalance: ch.Balance,
			AutoDiscount:   auto.AutoDiscount,
			AutoMatched:    auto.Matched,
			AutoRecognized: auto.Recognized,
		}
		if m, ok := mapByCh[int(ch.ID)]; ok {
			view.MappingID = m.ID
			view.VendorCode = m.VendorCode
			view.VendorName = vdByCode[m.VendorCode]
			view.Discount = m.Discount
			view.DiscountOverride = m.DiscountOverride
			view.Remark = m.Remark
		} else {
			// 未映射: final discount = auto (用户可在前端手改)
			view.Discount = auto.AutoDiscount
		}
		views = append(views, view)
	}
	ok(c, gin.H{
		"items": views,
		"total": len(views),
	})
}

// assignChannelVendor POST /api/channel-mappings
// 把渠道分配给供应商 (1:1 关系, 1 渠道最多挂 1 个)
// 如果已存在映射, 替换 vendor_code
type AssignChannelVendorRequest struct {
	ChannelID  int     `json:"channel_id" binding:"required"`
	VendorCode string  `json:"vendor_code" binding:"required"`
	Discount   float64 `json:"discount"` // final discount; 0 = 用 auto
	Remark     string  `json:"remark"`
}

func (s *Server) assignChannelVendor(c *gin.Context) {
	var req AssignChannelVendorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errResp(c, 400, "invalid body: "+err.Error(), nil)
		return
	}
	// discount=0 → 用 auto
	if req.Discount <= 0 {
		chs, _ := dal.ListChannels(c.Request.Context(), 0)
		for _, ch := range chs {
			if int(ch.ID) == req.ChannelID {
				auto := discount.ParseDiscountFromName(ch.Name)
				req.Discount = auto.AutoDiscount
				break
			}
		}
		if req.Discount <= 0 {
			req.Discount = 1.0 // 未识别默认原价
		}
	}
	if err := dal.UpdateChannelVendorAssignment(c.Request.Context(), req.ChannelID, req.VendorCode, req.Discount); err != nil {
		errResp(c, 500, "assign failed: "+err.Error(), nil)
		return
	}
	if s.audit != nil {
		_ = s.audit.Log(c, "channel_mapping.assign", "channel",
			fmt.Sprintf("%d", req.ChannelID), "assigned to vendor", map[string]interface{}{
				"vendor_code": req.VendorCode,
				"discount":    req.Discount,
			})
	}
	ok(c, gin.H{"channel_id": req.ChannelID, "vendor_code": req.VendorCode, "discount": req.Discount})
}

// unassignChannelVendor DELETE /api/channel-mappings/:channel_id
// 解除渠道的供应商映射 (整行删除)
func (s *Server) unassignChannelVendor(c *gin.Context) {
	chID := parseInt(c.Param("channel_id"))
	if chID == 0 {
		errResp(c, 400, "invalid channel_id", nil)
		return
	}
	m, err := dal.GetChannelVendorByChannelID(c.Request.Context(), chID)
	if err != nil {
		errResp(c, 500, "lookup failed: "+err.Error(), nil)
		return
	}
	if m == nil {
		errResp(c, 404, "no mapping for this channel", nil)
		return
	}
	if err := dal.DeleteChannelVendor(c.Request.Context(), m.ID); err != nil {
		errResp(c, 500, "delete failed: "+err.Error(), nil)
		return
	}
	if s.audit != nil {
		_ = s.audit.Log(c, "channel_mapping.unassign", "channel",
			fmt.Sprintf("%d", chID), "unassigned", nil)
	}
	ok(c, gin.H{"channel_id": chID, "unassigned": true})
}

// correctChannelDiscount POST /api/channel-mappings/:channel_id/correct-discount
// 矫正折扣 (人工覆盖, set discount_override=true)
type CorrectChannelDiscountRequest struct {
	Discount float64 `json:"discount" binding:"required,min=0,max=1"`
	Remark   string  `json:"remark"`
}

func (s *Server) correctChannelDiscount(c *gin.Context) {
	chID := parseInt(c.Param("channel_id"))
	if chID == 0 {
		errResp(c, 400, "invalid channel_id", nil)
		return
	}
	var req CorrectChannelDiscountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errResp(c, 400, "invalid body: "+err.Error(), nil)
		return
	}
	m, err := dal.GetChannelVendorByChannelID(c.Request.Context(), chID)
	if err != nil {
		errResp(c, 500, "lookup failed: "+err.Error(), nil)
		return
	}
	if m == nil {
		errResp(c, 404, "no mapping, please assign vendor first", nil)
		return
	}
	if err := dal.UpdateChannelVendorDiscount(c.Request.Context(), m.ID, req.Discount, req.Remark); err != nil {
		errResp(c, 500, "update failed: "+err.Error(), nil)
		return
	}
	if s.audit != nil {
		_ = s.audit.Log(c, "channel_mapping.correct_discount", "channel",
			fmt.Sprintf("%d", chID), "discount corrected", map[string]interface{}{
				"new_discount": req.Discount,
				"remark":       req.Remark,
			})
	}
	ok(c, gin.H{"channel_id": chID, "discount": req.Discount, "discount_override": true})
}

// reparseAllChannelsDiscounts POST /api/channel-mappings/reparse
// 触发重新解析所有渠道名 → 更新 auto_discount 字段
// 不覆盖 discount_override=true 的行
func (s *Server) reparseAllChannelsDiscounts(c *gin.Context) {
	ctx := c.Request.Context()
	chs, err := dal.ListChannels(ctx, 0)
	if err != nil {
		errResp(c, 500, "list channels failed: "+err.Error(), nil)
		return
	}
	updated := 0
	for _, ch := range chs {
		auto := discount.ParseDiscountFromName(ch.Name)
		// 找 mapping
		m, _ := dal.GetChannelVendorByChannelID(ctx, int(ch.ID))
		if m == nil {
			continue // 未映射的, 跳过
		}
		if m.DiscountOverride {
			continue // 人工矫正过的, 不动
		}
		// 用新 auto 覆盖 discount + auto_discount
		updates := map[string]interface{}{
			"auto_discount":   auto.AutoDiscount,
			"auto_matched":    auto.Matched,
			"auto_recognized": auto.Recognized,
			"discount":        auto.AutoDiscount,
			"updated_at":      gorm.Expr("NOW()"),
		}
		if err := dal.OPS.WithContext(ctx).Model(&dal.ChannelVendorMap{}).
			Where("id = ?", m.ID).Updates(updates).Error; err != nil {
			errResp(c, 500, "update failed: "+err.Error(), nil)
			return
		}
		updated++
	}
	if s.audit != nil {
		_ = s.audit.Log(c, "channel_mapping.reparse_all", "channel", "all", "reparse all", map[string]interface{}{
			"updated": updated,
		})
	}
	ok(c, gin.H{"updated": updated, "total_channels": len(chs)})
}

// ===== upstream 渠道列表（只读，来源 newapi DB） =====

func (s *Server) listupstreamChannels(c *gin.Context) {
	rows, err := dal.ListChannels(c.Request.Context(), 0)
	if err != nil {
		errResp(c, 500, "list channels failed", err.Error())
		return
	}
	// 不返回 key（敏感信息）
	type safeChannel struct {
		ID                 int     `json:"id"`
		Name               string  `json:"name"`
		Type               int     `json:"type"`
		Status             int     `json:"status"`
		Models             string  `json:"models"`
		Group              string  `json:"group"`
		UsedQuota          int64   `json:"used_quota"`
		Balance            float64 `json:"balance"`
		BalanceUpdatedTime int64   `json:"balance_updated_time"`
		ResponseTime       int     `json:"response_time"`
	}
	out := make([]safeChannel, 0, len(rows))
	for _, r := range rows {
		out = append(out, safeChannel{
			ID: r.ID, Name: r.Name, Type: r.Type, Status: r.Status,
			Models: r.Models, Group: r.Group, UsedQuota: r.UsedQuota,
			Balance: r.Balance, BalanceUpdatedTime: r.BalanceUpdatedTime,
			ResponseTime: r.ResponseTime,
		})
	}
	ok(c, out)
}

func (s *Server) getupstreamChannel(c *gin.Context) {
	id := parseInt(c.Param("id"))
	ch, err := dal.GetChannel(c.Request.Context(), id)
	if err != nil {
		errResp(c, 500, "get channel failed", err.Error())
		return
	}
	if ch == nil {
		errResp(c, 404, "channel not found", nil)
		return
	}
	// 屏蔽 key
	ch.Key = ""
	ok(c, ch)
}

// 静默 unused import warning
var (
	_ = fmt.Sprintf
	_ = strconv.Itoa
	_ = http.StatusOK
)

func jsonMarshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}
