// Package ai: 顶层 Diagnose —— KB 优先 + LLM 兜底
//
// 流程：
//  1. KB 匹配（patterns × log.content）→ 命中且 confidence >= 0.6 → 直接返回 source=kb
//  2. KB 未命中或 confidence < 0.6 → 调 LLM（Factory 创建的 Provider）
//     - 5min Redis 缓存（key=channel:model:pattern_hash）
//     - 落 ai_diagnoses
//  3. LLM 未配置（Provider == nil）→ 走 kb_fallback（仅 KB 命中 + 提示 "no LLM configured"）
//  4. 关联：把 diagnosis.ID 写回 cluster.DiagnosisID
package ai

import (
	"context"
	"log"

	"github.com/api-ops/api-ops/internal/ai/kb"
	"github.com/api-ops/api-ops/internal/ai/llm"
	"github.com/api-ops/api-ops/internal/dal"
)

// confidenceThreshold KB 命中即返回的最小 confidence
const confidenceThreshold = 0.6

// Diagnose 诊断单个 cluster
//   - cluster: ai_error_clusters 中的一行（含 SampleContent / Pattern / ChannelID / ModelName / Count）
//   - 返回 *dal.AIDiagnosis（已写入 DB）
func Diagnose(ctx context.Context, cluster *dal.AIErrorCluster) (*dal.AIDiagnosis, error) {
	// 1) KB 匹配
	kbEntry, kbScore := kb.Match(ctx, cluster.SampleContent)
	if kbEntry != nil && kbScore >= confidenceThreshold {
		d := &dal.AIDiagnosis{
			Pattern:    cluster.Pattern,
			ChannelID:  cluster.ChannelID,
			ModelName:  cluster.ModelName,
			Source:     "kb",
			Confidence: kbScore,
			Category:   kbEntry.Category,
			Severity:   kbEntry.Severity,
			RootCause:  kbEntry.RootCause,
			Action:     kbEntry.Action,
			DocURL:     kbEntry.DocURL,
			KBEntryID:  &kbEntry.ID,
		}
		if err := dal.CreateAIDiagnosis(ctx, d); err != nil {
			return nil, err
		}
		linkCluster(ctx, cluster.ID, d.ID)
		return d, nil
	}

	// 2) LLM 路径
	provider := llm.Factory(ctx)
	if provider == nil {
		// 无 LLM 配置 → 纯 KB 路径
		d := kbFallbackDiag(cluster, kbEntry, kbScore, "no LLM configured; KB 弱匹配或无匹配", "1) 配置 system_config.ai_provider 启用 LLM；2) 扩充 error_kb_entries")
		if err := dal.CreateAIDiagnosis(ctx, d); err != nil {
			return nil, err
		}
		linkCluster(ctx, cluster.ID, d.ID)
		return d, nil
	}

	sample := llm.ErrorSample{
		Pattern:       cluster.Pattern,
		SampleContent: cluster.SampleContent,
		ChannelID:     cluster.ChannelID,
		ModelName:     cluster.ModelName,
		Count:         cluster.Count,
	}
	diag, err := llm.DiagnoseWithCache(ctx, provider, sample)
	if err != nil {
		log.Printf("[ai.diagnose] llm call failed pattern=%q: %v (fall back to kb)", cluster.Pattern, err)
		d := kbFallbackDiag(cluster, kbEntry, kbScore, "LLM 调用失败；KB 弱匹配", "1) 检查 LLM 配置；2) 手动排查 error 日志")
		if err2 := dal.CreateAIDiagnosis(ctx, d); err2 != nil {
			return nil, err2
		}
		linkCluster(ctx, cluster.ID, d.ID)
		return d, nil
	}

	d := &dal.AIDiagnosis{
		Pattern:     cluster.Pattern,
		ChannelID:   cluster.ChannelID,
		ModelName:   cluster.ModelName,
		Source:      diag.Source,
		Confidence:  diag.Confidence,
		Category:    diag.Category,
		Severity:    diag.Severity,
		RootCause:   diag.RootCause,
		Action:      diag.Action,
		DocURL:      diag.DocURL,
		LLMProvider: diag.Provider,
		LLMTokens:   diag.Tokens,
	}
	if err := dal.CreateAIDiagnosis(ctx, d); err != nil {
		return nil, err
	}
	linkCluster(ctx, cluster.ID, d.ID)
	return d, nil
}

func linkCluster(ctx context.Context, clusterID, diagID uint64) {
	if clusterID == 0 {
		return
	}
	if err := dal.OPS.WithContext(ctx).Model(&dal.AIErrorCluster{}).
		Where("id = ?", clusterID).
		Update("diagnosis_id", diagID).Error; err != nil {
		log.Printf("[ai.diagnose] link cluster %d → diagnosis %d failed: %v", clusterID, diagID, err)
	}
}

// kbFallbackDiag 构造 KB/LLM-fallback 诊断行（无 LLM / LLM 失败）
func kbFallbackDiag(c *dal.AIErrorCluster, kb *dal.ErrorKBEntry, score float64, rootCause, action string) *dal.AIDiagnosis {
	d := &dal.AIDiagnosis{
		Pattern: c.Pattern, ChannelID: c.ChannelID, ModelName: c.ModelName,
		Source: "kb_fallback", Confidence: score,
		Category: "未分类", Severity: "warning", RootCause: rootCause, Action: action,
	}
	if kb != nil {
		if kb.Category != "" {
			d.Category = kb.Category
		}
		if kb.Severity != "" {
			d.Severity = kb.Severity
		}
		if kb.DocURL != "" {
			d.DocURL = kb.DocURL
		}
		id := kb.ID
		d.KBEntryID = &id
	}
	return d
}
