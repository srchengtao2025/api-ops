// Package kb: 错误知识库（KB）—— YAML 文件 → DB upsert + 匹配
//   - 4 个 YAML 文件（aws_bedrock / provider_gamma / openai / anthropic）
//   - Loader 从 embed FS 读取，启动时全部 upsert 到 error_kb_entries 表
//   - Match()：error log 文本 ↔ 任一 pattern 关键字（不区分大小写），score ∈ [0, 1]
package kb

import (
	"context"
	"embed"
	"encoding/json"
	"log"
	"strings"

	"github.com/api-ops/api-ops/internal/dal"
	"gopkg.in/yaml.v3"
)

//go:embed data/*.yaml
var embeddedYAML embed.FS

// EntryYAML YAML 单条结构
type EntryYAML struct {
	Code       string   `yaml:"code"`
	HTTPStatus int      `yaml:"http_status"`
	Category   string   `yaml:"category"`
	Severity   string   `yaml:"severity"`
	RootCauses []string `yaml:"root_causes"`
	Patterns   []string `yaml:"patterns"`
	Actions    []string `yaml:"actions"`
	DocURL     string   `yaml:"doc_url"`
}

// LoadAll 从 embed FS 加载所有 YAML 并 upsert 到 DB
func LoadAll(ctx context.Context) (int, error) {
	files, err := embeddedYAML.ReadDir("data")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
			continue
		}
		vendor := strings.TrimSuffix(f.Name(), ".yaml")
		data, err := embeddedYAML.ReadFile("data/" + f.Name())
		if err != nil {
			log.Printf("[kb] read %s failed: %v", vendor, err)
			continue
		}
		n, err := parseAndUpsert(ctx, vendor, data)
		if err != nil {
			log.Printf("[kb] load %s failed: %v", vendor, err)
			continue
		}
		count += n
		log.Printf("[kb] loaded vendor=%s entries=%d", vendor, n)
	}
	return count, nil
}

func parseAndUpsert(ctx context.Context, vendor string, data []byte) (int, error) {
	var entries []EntryYAML
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		row := &dal.ErrorKBEntry{
			Vendor:    vendor,
			ErrorCode: e.Code,
			Category:  e.Category,
			Severity:  ifEmpty(e.Severity, "warning"),
			RootCause: strings.Join(e.RootCauses, "；"),
			Action:    strings.Join(e.Actions, "；"),
			DocURL:    e.DocURL,
			Patterns:  marshalJSONArr(e.Patterns),
			Source:    "yaml:" + vendor,
			Enabled:   true,
		}
		if err := dal.UpsertErrorKB(ctx, row); err != nil {
			log.Printf("[kb] upsert %s/%s failed: %v", vendor, e.Code, err)
			continue
		}
		n++
	}
	return n, nil
}

func ifEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func marshalJSONArr(arr []string) string {
	if len(arr) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(arr)
	return string(b)
}

// Match 在 error_kb_entries 中找最匹配的一条
//   - score = 命中 pattern 数 / 总 pattern 数（最高 1.0）
//   - 命中不到时返回 (nil, 0)
func Match(ctx context.Context, errorContent string) (*dal.ErrorKBEntry, float64) {
	if strings.TrimSpace(errorContent) == "" {
		return nil, 0
	}
	var entries []dal.ErrorKBEntry
	if err := dal.OPS.WithContext(ctx).Where("enabled = ?", true).Find(&entries).Error; err != nil {
		return nil, 0
	}
	lc := strings.ToLower(errorContent)
	var best *dal.ErrorKBEntry
	bestScore := 0.0
	for i := range entries {
		e := &entries[i]
		patterns := unmarshalPatterns(e.Patterns)
		if len(patterns) == 0 {
			patterns = []string{e.ErrorCode}
		}
		hit := 0
		for _, p := range patterns {
			if p == "" {
				continue
			}
			if strings.Contains(lc, strings.ToLower(p)) {
				hit++
			}
		}
		if hit == 0 {
			continue
		}
		score := float64(hit) / float64(len(patterns))
		if score > bestScore {
			bestScore = score
			best = e
		}
	}
	return best, bestScore
}

func unmarshalPatterns(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}
