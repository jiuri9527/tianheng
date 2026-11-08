package tests

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"llm-hub/src/hub"
)

func diskModelIDs(t *testing.T) map[string]bool {
	t.Helper()
	path, err := hub.FindRealModelsFile()
	if err != nil {
		t.Fatalf("找不到 data/models-real.json: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取真实数据文件失败: %v", err)
	}
	var env struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("解析真实数据文件失败: %v", err)
	}
	set := map[string]bool{}
	for _, m := range env.Data {
		if m.ID != "" {
			set[m.ID] = true
		}
	}
	return set
}

func TestRealDataCountsAndSource(t *testing.T) {
	list, err := hub.LoadRealModels()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(list) != 460 {
		t.Fatalf("真实模型数应为 460，实际 %d", len(list))
	}
	idset, err := hub.RealModelIDSet()
	if err != nil {
		t.Fatal(err)
	}
	if len(idset) != 460 {
		t.Fatalf("真实 id 集合应为 460，实际 %d", len(idset))
	}
	if !idset["liquid/lfm-3-1.7b"] || !idset["tencent/hy4"] {
		t.Fatal("真实样例 id 缺失")
	}
	// 真实数据集不含凭印象手写的模型名（C-1 硬性约束）
	for _, fake := range []string{"gpt-4o", "grok-3", "claude-3-7-sonnet"} {
		if idset[fake] {
			t.Fatalf("真实数据集不应含凭印象手写 id %q", fake)
		}
	}
	// 供应商前缀数
	prefixes := map[string]bool{}
	for _, m := range list {
		if m.ID == "" || m.OwnedBy == "" {
			t.Fatalf("存在空 id/供应商: %+v", m)
		}
		prefixes[m.OwnedBy] = true
	}
	if len(prefixes) != 69 {
		t.Fatalf("供应商前缀应为 69，实际 %d", len(prefixes))
	}
}

func TestRealDataEmbeddedMatchesDiskVerbatim(t *testing.T) {
	list, _ := hub.LoadRealModels()
	disk := diskModelIDs(t)
	if len(disk) != 460 {
		t.Fatalf("磁盘文件模型数应为 460，实际 %d", len(disk))
	}
	for _, m := range list {
		if !disk[m.ID] {
			t.Fatalf("嵌入副本含磁盘文件中不存在的 id %q（逐字副本被破坏）", m.ID)
		}
	}
	for id := range disk {
		found := false
		for _, m := range list {
			if m.ID == id {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("磁盘文件中的 id %q 未出现在嵌入副本中", id)
		}
	}
}

// 每个对外暴露的 /v1/models 模型 id 必须逐字属于 data/models-real.json。
func TestV1ModelsAllIDsAreReal(t *testing.T) {
	srv, _ := newServer(t, nil)
	_, sk := bootstrap(t, srv)
	rec := do(srv, "GET", "/v1/models", "", "Bearer "+sk)
	if rec.Code != 200 {
		t.Fatalf("/v1/models 失败: %d %s", rec.Code, rec.Body.String())
	}
	m := decode(t, rec)
	if m["object"] != "list" {
		t.Fatalf("object 应为 list: %v", m["object"])
	}
	list := asSlice(t, m["data"])
	if len(list) != 460 {
		t.Fatalf("/v1/models 应返回 460 条真实模型，实际 %d", len(list))
	}
	disk := diskModelIDs(t)
	for _, raw := range list {
		entry := asStringMap(t, raw)
		id, _ := entry["id"].(string)
		if id == "" || !disk[id] {
			t.Fatalf("/v1/models 暴露了不在 data/models-real.json 的 id %q", id)
		}
		if entry["object"] != "model" {
			t.Fatalf("每条 object 应为 model: %v", entry)
		}
		if entry["source"] != "data/models-real.json" {
			t.Fatalf("未标注真实来源: %v", entry["source"])
		}
	}
	for _, fake := range []string{"gpt-4o", "grok-3", "claude-3-7-sonnet"} {
		mustNotContain(t, rec.Body.String(), `"id":"`+fake+`"`)
	}
}

func TestOpenAIListVerbatimFields(t *testing.T) {
	list, err := hub.OpenAIList()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 460 {
		t.Fatalf("OpenAIList 应 460 条，实际 %d", len(list))
	}
	for _, m := range list {
		id, _ := m["id"].(string)
		if id == "" {
			t.Fatal("存在空 id")
		}
		if _, ok := m["context_length"]; !ok {
			t.Fatalf("缺少逐字 context_length 字段: %v", id)
		}
	}
}

func mustNotContain(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Fatalf("不应包含 %q", sub)
	}
}

// TestV1ModelByIDOnlyServesRealEntries 校验 GET /v1/models/{id} 只返回真实条目，
// 且含斜杠的真实 id（liquid/lfm-3-1.7b）能正确路由（{id...} 余段通配）。
func TestV1ModelByIDOnlyServesRealEntries(t *testing.T) {
	srv, _ := newServer(t, nil)
	jwt, sk := bootstrap(t, srv)

	// 取数据集第一条真实 id 与其字段，逐字比对响应
	list, err := hub.LoadRealModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		t.Fatal("真实模型数据集为空")
	}
	real := list[0]

	rec := do(srv, "GET", "/v1/models/"+real.ID, "", "Bearer "+sk)
	if rec.Code != 200 {
		t.Fatalf("真实 id %q 应 200，实际 %d %s", real.ID, rec.Code, rec.Body.String())
	}
	m := decode(t, rec)
	if m["id"] != real.ID {
		t.Fatalf("id 不符: %v != %v", m["id"], real.ID)
	}
	if m["object"] != "model" {
		t.Fatalf("object 应为 model，实际 %v", m["object"])
	}
	if m["source"] != "data/models-real.json" {
		t.Fatalf("source 标注缺失: %v", m["source"])
	}
	if int(m["context_length"].(float64)) != real.ContextLength {
		t.Fatalf("context_length 不符: %v != %d", m["context_length"], real.ContextLength)
	}

	// 未知 id 必须 404 model_not_found，绝不合成条目
	rec = do(srv, "GET", "/v1/models/definitely/not-a-real-model", "", "Bearer "+sk)
	if rec.Code != 404 {
		t.Fatalf("未知 id 应 404，实际 %d", rec.Code)
	}
	em := decode(t, rec)
	errObj, _ := em["error"].(map[string]interface{})
	if errObj["code"] != "model_not_found" {
		t.Fatalf("错误码应为 model_not_found，实际 %v", errObj["code"])
	}

	// 无令牌必须 401（数据面鉴权未被绕过）
	rec = do(srv, "GET", "/v1/models/"+real.ID, "", "")
	if rec.Code != 401 {
		t.Fatalf("无令牌应 401，实际 %d", rec.Code)
	}
	_ = jwt
}
