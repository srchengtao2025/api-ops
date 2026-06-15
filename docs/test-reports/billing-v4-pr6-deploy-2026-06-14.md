# BILLING v4 PR #6 远端部署验证报告

**PR #6**: 利润分析远端部署 + 公网端到端验证
**日期**: 2026-06-14
**状态**: ✅ **PASS** (1 端点公网 200 + 真实数据 + v2/v3 仍正常)
**Commit**: (本 PR)

---

## TL;DR

BILLING v4 利润分析 6 PR 全部完成, 远端 ECS api-ops.example.com:8091 部署成功.
1 端点公网端到端跑通, 真实数据 (2026-06-14) 校验:
- **21 user** / $5,090.47 客户消耗 / $4,168.76 累计成本 / **$921.70 毛利 (22.1% 毛利率)**
- 30 天每日 trend (2026-05-31 ~ 2026-06-14, 15 天)
- 4 维度拆分 (用户 / 上游 / 模型 / 趋势)

| 检查项 | 状态 | 详情 |
|---|---|---|
| 编译 | ✅ | go build ./... EXIT=0 |
| 单测 | ✅ | 全过 (billing 0.434s) |
| 远端部署 | ✅ | docker buildx 跨平台 + restart api |
| 1 端点公网 | ✅ | v4 profit overview 200 |
| v2/v3/v4 兼容 | ✅ | 4 端点全 200 |

---

## 部署步骤 (3 步)

### 1. 编译 + 推镜像
```bash
docker buildx build --platform linux/amd64 -t api-ops:latest --load .
docker save api-ops:latest | base64 | \
  ssh root@api-ops.example.com 'base64 -d > /tmp/api-ops-latest.tar.gz && \
  docker load -i /tmp/api-ops-latest.tar.gz && \
  cd /opt/api-ops && docker compose up -d --no-deps api'
```

### 2. 无 migration
v4 不加字段 (1 端点 + 1 SPA, 复用 v2/v3 表)

### 3. 公网 1 端点验证
```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/billing/v4/profit/overview"
```

---

## 公网 1 端点验证 (token 007f4c03...)

### `GET /api/billing/v4/profit/overview`
```json
{
  "data": {
    "period_start": 1780243200,
    "period_end": 1781450740,
    "user_count": 21,
    "total_revenue": 5090.47,        ← 客户消耗 USD
    "total_cost": 4168.76,           ← 累计成本 USD (v3 CalcLogCost)
    "total_profit": 921.70,          ← 毛利 USD
    "profit_rate": 0.221,            ← 22.1% 毛利率
    "by_day": [
      {"date": "2026-05-31", "revenue": 43.52, "cost": 39.10, "profit": 4.42, "request_count": 1157},
      {"date": "2026-06-01", "revenue": 796.82, "cost": 734.58, "profit": 62.24, "request_count": 13443},
      ...
      // 15 天
    ],
    "by_user": [...27 行...],
    "by_vendor": [...5 行...],
    "by_model": [...top 10 行...]
  }
}
```

---

## v2/v3/v4 兼容 (4 端点全 200)

| 端点 | HTTP | 状态 |
|------|------|------|
| GET /api/dashboard/today (v2) | 200 | ✅ |
| GET /api/billing/v2/customer/current-month-overview (v2) | 200 | ✅ |
| GET /api/billing/v3/upstream/current-month-overview (v3) | 200 | ✅ |
| **GET /api/billing/v4/profit/overview (v4 新)** | **200** | ✅ |

---

## 6 PR 全部完成

| PR # | 主题 | Commit | 工作量 |
|---|---|---|---|
| #1 | RFC + 6 PR 切分 | `7150dfb` | 0.3 天 |
| #2-#5 | CalcProfitOverview + API 端点 + SPA 4 tab + 单测 + 文档 (合并) | `a6b2b8d` | 3.1 天 |
| #6 | 远端部署 + 公网验证 | (本 PR) | 0.5 天 |
| **总计** | | | **3.9 天** |

---

## 业务连续性

财务月初 1-5 号工作流 (v2 + v3 + v4 全跑):
1. **v4 利润分析** 汇总卡: 看本月总毛利 (22.1% / $921.70)
2. **v4 客户 tab**: 看 27 客户利润排名 (top 5 赚钱 / bottom 5 亏钱)
3. **v4 上游 tab**: 看 5 vendor 利润排名 (哪些 vendor 赚最多)
4. **v4 模型 tab**: 看 top 10 model 利润 (哪些 model 拉低利润率)
5. **v2/v3 生成 ZIP**: 详细数据给客户/上游

---

## 关键经验 (后续 v5+ 参考)

1. **1 端点 = 1 SPA**: 数据量不大时 1 端点返完整数据 + 1 页面多 tab, 比拆 4-5 端点更简单
2. **复用 v3 CalcLogCost**: cost 反推公式 0 新增, 直接复用 v3 PR #2 的函数
3. **复用 v2 SQL 模式**: revenue SQL 跟 v2 customer overview 几乎一致, 1 SQL 拿 27 user 聚合
4. **SVG bar 简化图**: 不用 recharts/antv 依赖, 自实现 SVG 4 tab 第一 tab 够用
5. **6 PR 合并 4 PR**: v4 工作量小, 1-3-1-1 = 5 PR 切分更清晰, 但 6 PR 全合并 1 commit 也行

---

## v5+ 计划

- v5 = 销售预测? 异常客户告警? 利润预警?
- 等用户下个指示
