// Package models 从 go:embed 的 data/models-real.json 逐字副本加载真实模型池。
// 唯一数据源：抓取自 https://openrouter.ai/api/v1/models 的 460 条真实记录。
// 禁止在此新增/编造任何模型 id。
package models

import (
	_ "embed"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// SourceSHA256 返回 go:embed 的真实模型数据源字节的 SHA-256。
// 值由真实字节实时计算，绝不硬编码；供排行榜与真实性校验使用。
func SourceSHA256() string {
	sum := sha256.Sum256(realModelsJSON)
	return hex.EncodeToString(sum[:])
}

//go:embed models-real.json
var realModelsJSON []byte

// RealModel 是 data/models-real.json 中一条真实记录的投影，Raw 保留逐字原始对象。
type RealModel struct {
	ID                   string
	CanonicalSlug        string
	Name                 string
	Created              int64
	Description          string
	ContextLength        int
	ArchitectureModality string
	InputModalities      []string
	OutputModalities     []string
	PromptPricePerTok    float64
	CompletionPricePerTok float64
	MaxCompletionTokens  int
	SupportedParameters  []string
	OwnedBy              string // 由真实 id 前缀（供应商）派生，非编造
	Raw                  json.RawMessage
}

type rawEnvelope struct {
	Data       []json.RawMessage `json:"data"`
	TotalCount int               `json:"total_count"`
}

type rawModel struct {
	ID            string `json:"id"`
	CanonicalSlug string `json:"canonical_slug"`
	Name          string `json:"name"`
	Created       int64  `json:"created"`
	Description   string `json:"description"`
	ContextLength int    `json:"context_length"`
	Architecture  struct {
		Modality         string   `json:"modality"`
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	Pricing struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
	TopProvider struct {
		MaxCompletionTokens int `json:"max_completion_tokens"`
	} `json:"top_provider"`
	SupportedParameters []string `json:"supported_parameters"`
}

func parsePrice(s string) float64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

func ownedBy(id string) string {
	if i := strings.IndexByte(id, '/'); i > 0 {
		return id[:i]
	}
	return id
}

var (
	cacheMu    sync.Mutex
	cache      []RealModel
	cacheIDSet map[string]bool
)

// Load 解析嵌入的真实数据，返回全部真实模型（带缓存）。
func Load() ([]RealModel, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cache != nil {
		return cache, nil
	}
	var env rawEnvelope
	if err := json.Unmarshal(realModelsJSON, &env); err != nil {
		return nil, fmt.Errorf("解析 models-real.json 失败: %w", err)
	}
	out := make([]RealModel, 0, len(env.Data))
	idset := map[string]bool{}
	for _, raw := range env.Data {
		var rm rawModel
		if err := json.Unmarshal(raw, &rm); err != nil {
			return nil, err
		}
		if rm.ID == "" {
			continue
		}
		m := RealModel{
			ID:                    rm.ID,
			CanonicalSlug:         rm.CanonicalSlug,
			Name:                  rm.Name,
			Created:               rm.Created,
			Description:           rm.Description,
			ContextLength:         rm.ContextLength,
			ArchitectureModality:  rm.Architecture.Modality,
			InputModalities:       rm.Architecture.InputModalities,
			OutputModalities:      rm.Architecture.OutputModalities,
			PromptPricePerTok:     parsePrice(rm.Pricing.Prompt),
			CompletionPricePerTok: parsePrice(rm.Pricing.Completion),
			MaxCompletionTokens:   rm.TopProvider.MaxCompletionTokens,
			SupportedParameters:   rm.SupportedParameters,
			OwnedBy:               ownedBy(rm.ID),
			Raw:                   raw, // 逐字原始对象
		}
		out = append(out, m)
		idset[m.ID] = true
	}
	cache = out
	cacheIDSet = idset
	return cache, nil
}

// IDSet 返回真实模型 id 集合，供真实性校验测试使用。
func IDSet() (map[string]bool, error) {
	if _, err := Load(); err != nil {
		return nil, err
	}
	return cacheIDSet, nil
}

// OpenAIList 把真实模型池转成 OpenAI 兼容 /v1/models 的 data 数组。
// 每条以逐字原始对象为基础，仅补充 object:"model" 与 owned_by（由真实 id 前缀派生）。
func OpenAIList() ([]map[string]interface{}, error) {
	list, err := Load()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(list))
	for _, m := range list {
		obj := map[string]interface{}{}
		// 逐字展开原始字段
		var rawMap map[string]json.RawMessage
		_ = json.Unmarshal(m.Raw, &rawMap)
		for k, v := range rawMap {
			obj[k] = v
		}
		obj["id"] = m.ID // 逐字真实 id
		obj["object"] = "model"
		obj["owned_by"] = m.OwnedBy
		obj["source"] = "data/models-real.json"
		out = append(out, obj)
	}
	return out, nil
}

// FindRealModelsFile 从当前工作目录向上查找 data/models-real.json，供离线校验用。
func FindRealModelsFile() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 8; i++ {
		cand := filepath.Join(dir, "data", "models-real.json")
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("未找到 data/models-real.json")
}

// FindByID 在真实数据集中按 id 精确查找（大小写敏感，逐字匹配）。
// 用于 OpenAI 兼容的 GET /v1/models/{id}；未找到返回 ok=false，绝不合成条目。
func FindByID(id string) (RealModel, bool, error) {
	list, err := Load()
	if err != nil {
		return RealModel{}, false, err
	}
	for _, m := range list {
		if m.ID == id {
			return m, true, nil
		}
	}
	return RealModel{}, false, nil
}

// OpenAIObject 把单条真实记录转成 OpenAI 兼容的 model 对象（逐字字段 + object/owned_by/source）。
func OpenAIObject(m RealModel) map[string]interface{} {
	obj := map[string]interface{}{}
	var rawMap map[string]json.RawMessage
	_ = json.Unmarshal(m.Raw, &rawMap)
	for k, v := range rawMap {
		obj[k] = v
	}
	obj["id"] = m.ID
	obj["object"] = "model"
	obj["owned_by"] = m.OwnedBy
	obj["source"] = "data/models-real.json"
	return obj
}
