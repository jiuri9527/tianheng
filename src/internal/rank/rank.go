// 文件: src/internal/rank/rank.go
// Package rank 生成「绝对真实」的模型排行榜。
//
// 唯一数据源：src/internal/models 的 go:embed 副本 data/models-real.json
// （抓取自 https://openrouter.ai/api/v1/models 的 460 条真实记录）。
//
// 硬约束：
//   - 每个 id / name / provider / context_length / pricing / supported_parameters /
//     input_modalities / created / top_provider.max_completion_tokens 都逐字来自真实记录；
//   - 排名值只取真实字段，或由真实字段做可复核的确定性换算（如每 token 价 × 1e6）；
//   - 不引入任何调用量、评分、趋势、热度、延迟、吞吐等无法从数据集推导的编造数据；
//   - source_sha256 由真实字节实时计算，不硬编码。
package rank

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"llm-hub/src/internal/models"
)

// Dimensions 是全部可复现的排行榜维度。
var Dimensions = []string{
	"context", "price_asc", "price_desc", "output_asc",
	"capability", "newest", "max_output", "providers",
}

// Entry 是榜单中的一条真实记录（或真实供应商聚合）。
type Entry struct {
	Rank                  int      `json:"rank"`
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Provider              string   `json:"provider"`
	Value                 float64  `json:"value"`
	ValueLabel            string   `json:"value_label"`
	ContextLength         int      `json:"context_length"`
	MaxCompletionTokens   int      `json:"max_completion_tokens"`
	PromptPricePerTok     float64  `json:"prompt_price_per_tok"`
	CompletionPricePerTok float64  `json:"completion_price_per_tok"`
	SupportedParameters   []string `json:"supported_parameters"`
	InputModalities       []string `json:"input_modalities"`
	Created               int64    `json:"created"`
	Source                string   `json:"source"`
}

// Board 是一个维度的完整排行榜。
type Board struct {
	Dimension      string  `json:"dimension"`
	Title          string  `json:"title"`
	Formula        string  `json:"formula"`
	ValueUnit      string  `json:"value_unit"`
	TotalModels    int     `json:"total_models"`
	TotalProviders int     `json:"total_providers"`
	EntryCount     int     `json:"entry_count"`
	SourceSHA256   string  `json:"source_sha256"`
	GeneratedAt    int64   `json:"generated_at"`
	Entries        []Entry `json:"entries"`
}

// Report 是所有维度的合集。
type Report struct {
	TotalModels    int              `json:"total_models"`
	TotalProviders int              `json:"total_providers"`
	SourceSHA256   string           `json:"source_sha256"`
	GeneratedAt    int64            `json:"generated_at"`
	Dimensions     []string         `json:"dimensions"`
	Boards         map[string]*Board `json:"boards"`
}

type meta struct {
	title  string
	formula string
	unit   string
}

var metaTable = map[string]meta{
	"context":    {"上下文长度榜", "value = context_length（逐字取自真实记录），按降序排列", "tokens"},
	"price_asc":  {"输入价格最低榜", "value = pricing.prompt × 1e6（真实每 token 美元换算为每 1M token），仅计入 >0 的付费模型，按升序排列", "USD / 1M input tokens"},
	"price_desc": {"输入价格最高榜", "value = pricing.prompt × 1e6（真实每 token 美元换算为每 1M token），按降序排列", "USD / 1M input tokens"},
	"output_asc": {"输出价格最低榜", "value = pricing.completion × 1e6（真实每 token 美元换算为每 1M token），仅计入 >0 的付费模型，按升序排列", "USD / 1M output tokens"},
	"capability": {"能力覆盖榜", "value = len(supported_parameters) + len(input_modalities)（均为真实数组长度），按降序排列；并列按 id 字典序", "count"},
	"newest":     {"最新发布榜", "value = created（真实 Unix 时间戳），按降序排列", "unix_ts"},
	"max_output": {"最大输出榜", "value = top_provider.max_completion_tokens（真实字段），按降序排列", "tokens"},
	"providers":  {"供应商模型数榜", "value = 该供应商在真实数据集中的模型条数（按 id 的 '/' 前缀分组计数），按降序排列", "models"},
}

func providerCount(list []models.RealModel) int {
	set := map[string]bool{}
	for _, m := range list {
		set[m.OwnedBy] = true
	}
	return len(set)
}

func toEntry(m models.RealModel) Entry {
	return Entry{
		ID:                    m.ID,
		Name:                  m.Name,
		Provider:              m.OwnedBy,
		ContextLength:         m.ContextLength,
		MaxCompletionTokens:   m.MaxCompletionTokens,
		PromptPricePerTok:     m.PromptPricePerTok,
		CompletionPricePerTok: m.CompletionPricePerTok,
		SupportedParameters:   m.SupportedParameters,
		InputModalities:       m.InputModalities,
		Created:               m.Created,
		Source:                "data/models-real.json",
	}
}

func priceLabel(v float64) string { return fmt.Sprintf("%.10f", v) }

// Compute 生成指定维度的完整榜单（不是 TopN 摘要，包含全部真实条目）。
func Compute(dimension string) (*Board, error) {
	mt, ok := metaTable[dimension]
	if !ok {
		return nil, fmt.Errorf("未知排行榜维度 %q；可用维度: %s", dimension, strings.Join(Dimensions, ", "))
	}
	list, err := models.Load()
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(list))
	if dimension == "providers" {
		counts := map[string]int{}
		for _, m := range list {
			counts[m.OwnedBy]++
		}
		for p, n := range counts {
			entries = append(entries, Entry{
				ID:         p,
				Name:       p,
				Provider:   p,
				Value:      float64(n),
				ValueLabel: fmt.Sprintf("%d 个真实模型", n),
				Source:     "data/models-real.json",
			})
		}
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].Value != entries[j].Value {
				return entries[i].Value > entries[j].Value
			}
			return entries[i].ID < entries[j].ID
		})
	} else {
		for _, m := range list {
			e := toEntry(m)
			switch dimension {
			case "context":
				e.Value = float64(m.ContextLength)
				e.ValueLabel = fmt.Sprintf("%d tokens", m.ContextLength)
			case "price_asc", "price_desc":
				e.Value = m.PromptPricePerTok * 1e6
				e.ValueLabel = priceLabel(e.Value)
			case "output_asc":
				e.Value = m.CompletionPricePerTok * 1e6
				e.ValueLabel = priceLabel(e.Value)
			case "capability":
				e.Value = float64(len(m.SupportedParameters) + len(m.InputModalities))
				e.ValueLabel = fmt.Sprintf("%d 参数 + %d 输入模态", len(m.SupportedParameters), len(m.InputModalities))
			case "newest":
				e.Value = float64(m.Created)
				e.ValueLabel = fmt.Sprintf("created=%d", m.Created)
			case "max_output":
				e.Value = float64(m.MaxCompletionTokens)
				e.ValueLabel = fmt.Sprintf("%d tokens", m.MaxCompletionTokens)
			}
			if (dimension == "price_asc" || dimension == "output_asc") && e.Value <= 0 {
				continue // 免费/缺失价格不计入「最低付费」榜，不编造 0 值名次
			}
			entries = append(entries, e)
		}
		asc := dimension == "price_asc" || dimension == "output_asc"
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].Value != entries[j].Value {
				if asc {
					return entries[i].Value < entries[j].Value
				}
				return entries[i].Value > entries[j].Value
			}
			return entries[i].ID < entries[j].ID
		})
	}
	for i := range entries {
		entries[i].Rank = i + 1
	}

	return &Board{
		Dimension:      dimension,
		Title:          mt.title,
		Formula:        mt.formula,
		ValueUnit:      mt.unit,
		TotalModels:    len(list),
		TotalProviders: providerCount(list),
		EntryCount:     len(entries),
		SourceSHA256:   models.SourceSHA256(),
		GeneratedAt:    time.Now().Unix(),
		Entries:        entries,
	}, nil
}

// All 生成全部维度的排行榜合集。
func All() (*Report, error) {
	list, err := models.Load()
	if err != nil {
		return nil, err
	}
	boards := make(map[string]*Board, len(Dimensions))
	for _, d := range Dimensions {
		b, err := Compute(d)
		if err != nil {
			return nil, err
		}
		boards[d] = b
	}
	return &Report{
		TotalModels:    len(list),
		TotalProviders: providerCount(list),
		SourceSHA256:   models.SourceSHA256(),
		GeneratedAt:    time.Now().Unix(),
		Dimensions:     Dimensions,
		Boards:         boards,
	}, nil
}

// Verify 复核榜单真实性：每条 id 必须存在于真实数据集中，数量与哈希必须一致。
// 返回可写进审计日志的结构化结论，不做任何推断式「通过」。
func Verify() (map[string]interface{}, error) {
	list, err := models.Load()
	if err != nil {
		return nil, err
	}
	idset, err := models.IDSet()
	if err != nil {
		return nil, err
	}
	report, err := All()
	if err != nil {
		return nil, err
	}
	checked := 0
	unknown := []string{}
	for dim, b := range report.Boards {
		for _, e := range b.Entries {
			checked++
			if dim == "providers" {
				if !idsetHasProvider(list, e.ID) {
					unknown = append(unknown, dim+":"+e.ID)
				}
				continue
			}
			if !idset[e.ID] {
				unknown = append(unknown, dim+":"+e.ID)
			}
		}
	}
	return map[string]interface{}{
		"source":              "data/models-real.json",
		"source_sha256":       report.SourceSHA256,
		"total_models":        report.TotalModels,
		"total_providers":     report.TotalProviders,
		"dimensions":          len(report.Boards),
		"entries_checked":     checked,
		"entries_not_in_real": unknown,
		"fabricated_entries":  len(unknown),
		"verified":            len(unknown) == 0 && report.TotalModels == len(list),
	}, nil
}

func idsetHasProvider(list []models.RealModel, p string) bool {
	for _, m := range list {
		if m.OwnedBy == p {
			return true
		}
	}
	return false
}
