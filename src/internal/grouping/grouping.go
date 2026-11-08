// Package grouping 原生实现「分组抽象 + 条件规则」原理。
//
// ── 原理来源 ────────────────────────────────────────────────────────────────
// 取自 AI 供应商聚合层 Magpie（com.yetone.magpie）的 providers.json：
//
//	groups: [
//	  { id: "cheap-first", name: "便宜优先", disabled: true,
//	    members: [...3],
//	    rules: [ {"use":"stepfun-cn/step-5-preview","effort":"high"},
//	             {"use":"stepfun-cn/step-5-preview","images":true} ] },
//	  { id: "auto-dfmodel", name: "DeepSeek V4.1 flash", members: [...54] }
//	]
//
// 其路由决策里还把整个分组快照写进每条日志：
//
//	group: {id, ref:"auto/dfmodel", name, routing:"", affinity:"", members:[...], subs:[...]}
//
// 提炼出的原理有三条：
//  1. **逻辑名与物理成员解耦**：调用方只说 `auto/dfmodel`，由分组展开成几十个
//     (上游, 模型) 组合。上游增减只改分组，调用方的模型名永不变。
//  2. **子分组可嵌套**：`subs[]` 让「大分组」按能力再切分（如 flash / pro），
//     子分组自身也是可寻址的逻辑名。
//  3. **条件规则优先于轮询**：`rules[]` 按请求特征（effort / images / …）把某个
//     成员**提到最前**，而不是取消其余候选 —— 既满足「有图片就用视觉模型」这类
//     硬需求，又保留失败切换能力。
//
// ── 天衡的落法 ──────────────────────────────────────────────────────────────
// 天衡的渠道是用户众包贡献的、运行时动态出现，因此成员不能写死成上游账号，
// 而是**匹配表达式**（glob）：可精确到渠道、也可按模型 id 通配。
//
//	成员写法             含义
//	chan_ab12cd/deepseek-v4.1-flash   指定渠道 + 指定上游模型
//	*/deepseek-*                      任意渠道上的 deepseek 系列（真实 id 通配）
//	openai/gpt-*                      渠道 provider_type 为 openai 且模型匹配
//	auto/*                            任意渠道任意模型（等价于不设限）
//
// 本包为纯逻辑：不依赖 store、不发起网络请求，便于单测与复用。
package grouping

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"llm-hub/src/internal/fsutil"
)

// Rule 是一条条件规则：当请求特征满足全部条件时，把 Use 指定的成员提到候选最前。
//
// 字段语义与原理来源一致：空字段 = 该条件不参与判断（而非「等于零值」）。
type Rule struct {
	Use            string `json:"use"`              // 成员表达式，如 "stepfun-cn/step-5-preview"
	Effort         string `json:"effort,omitempty"` // 推理档位：low|medium|high|xhigh|max
	Images         *bool  `json:"images,omitempty"` // 请求是否含图片
	Tools          *bool  `json:"tools,omitempty"`  // 请求是否声明工具
	Stream         *bool  `json:"stream,omitempty"` // 是否流式
	MinInputTokens int    `json:"min_input_tokens,omitempty"`
	MaxInputTokens int    `json:"max_input_tokens,omitempty"`
	// Note 是给运维看的说明，不参与匹配。
	Note string `json:"note,omitempty"`
}

// SubRef 是一个嵌套子分组引用。
type SubRef struct {
	ID   string `json:"id"`
	Ref  string `json:"ref"`
	Name string `json:"name"`
	In   string `json:"in,omitempty"`
}

// Group 是一个逻辑分组。
type Group struct {
	ID       string   `json:"id"`
	Ref      string   `json:"ref"`  // 对外可寻址名，如 auto/dfmodel
	Name     string   `json:"name"` // 展示名
	Routing  string   `json:"routing,omitempty"`
	Affinity string   `json:"affinity,omitempty"` // "" 默认 | "pin" | "off"
	Disabled bool     `json:"disabled,omitempty"`
	Members  []string `json:"members"`
	Subs     []SubRef `json:"subs,omitempty"`
	Rules    []Rule   `json:"rules,omitempty"`
}

// Features 是一次真实请求的可匹配特征。
type Features struct {
	Effort      string `json:"effort,omitempty"`
	HasImages   bool   `json:"has_images"`
	HasTools    bool   `json:"has_tools"`
	Stream      bool   `json:"stream"`
	InputTokens int    `json:"input_tokens"`
}

// Catalog 是全部分组的目录。
type Catalog struct {
	Format  string  `json:"format"`
	Version int     `json:"version"`
	Groups  []Group `json:"groups"`
}

// Selection 是一次分组解析的真实结果，供路由日志与面板展示。
type Selection struct {
	GroupID   string   `json:"group_id"`
	GroupRef  string   `json:"group_ref"`
	GroupName string   `json:"group_name"`
	Affinity  string   `json:"affinity,omitempty"`
	Members   []string `json:"members"`          // 展开后的成员（含子分组）
	Rules     []string `json:"rules_applied"`    // 实际命中的规则描述
	Why       string   `json:"why"`              // 命中原由：rule|default|no_members
}

// NewCatalog 构造一个空目录。
func NewCatalog() *Catalog {
	return &Catalog{Format: "tianheng-groups", Version: 1}
}

// Add 追加/替换一个分组（按 ID 去重）。
func (c *Catalog) Add(g Group) {
	for i := range c.Groups {
		if c.Groups[i].ID == g.ID {
			c.Groups[i] = g
			return
		}
	}
	c.Groups = append(c.Groups, g)
}

// Get 按 ID 或 Ref 查找分组（两者都可寻址，与原理中的 group/ref 双名一致）。
func (c *Catalog) Get(name string) (Group, bool) {
	for _, g := range c.Groups {
		if g.ID == name || g.Ref == name {
			return g, true
		}
	}
	return Group{}, false
}

// List 返回全部分组（按 Ref 排序，便于面板稳定展示）。
func (c *Catalog) List() []Group {
	out := append([]Group(nil), c.Groups...)
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// Members 返回展开后的全部成员表达式：自身 members + 递归展开的子分组 members。
// 带环检测，环上重复出现的子分组只展开一次。
func (c *Catalog) Members(name string) ([]string, error) {
	return c.members(name, map[string]bool{})
}

func (c *Catalog) members(name string, seen map[string]bool) ([]string, error) {
	g, ok := c.Get(name)
	if !ok {
		return nil, fmt.Errorf("grouping: 分组不存在: %s", name)
	}
	if seen[g.ID] {
		return nil, nil
	}
	seen[g.ID] = true

	out := append([]string(nil), g.Members...)
	for _, sub := range g.Subs {
		key := sub.ID
		if key == "" {
			key = sub.Ref
		}
		more, err := c.members(key, seen)
		if err != nil {
			// 子分组缺失不致命：记录并继续（真实部署里分组会被逐步补全）。
			continue
		}
		out = append(out, more...)
	}
	return out, nil
}

// Resolve 解析一次请求：返回该分组的成员列表、实际命中的规则说明与判定依据。
//
// 规则语义：按声明顺序取**第一条全部条件满足**的规则，把它的 Use 成员提到最前；
// 其余成员顺序不变 —— 保留失败切换能力（与原理来源的行为一致：规则只改变首选，
// 不消灭候选）。
func (c *Catalog) Resolve(name string, feat Features) (Selection, error) {
	g, ok := c.Get(name)
	if !ok {
		return Selection{}, fmt.Errorf("grouping: 分组不存在: %s", name)
	}
	members, err := c.Members(name)
	if err != nil {
		return Selection{}, err
	}
	sel := Selection{
		GroupID: g.ID, GroupRef: g.Ref, GroupName: g.Name,
		Affinity: g.Affinity, Members: members,
		Rules: []string{}, Why: "default",
	}
	if len(members) == 0 {
		sel.Why = "no_members"
		return sel, nil
	}
	for _, r := range g.Rules {
		if !r.Match(feat) {
			continue
		}
		sel.Why = "rule"
		sel.Rules = append(sel.Rules, describeRule(r))
		sel.Members = pinFirst(members, r.Use)
		break
	}
	return sel, nil
}

// Match 判断规则的全部已声明条件是否满足。
func (r Rule) Match(f Features) bool {
	if r.Effort != "" && !strings.EqualFold(r.Effort, f.Effort) {
		return false
	}
	if r.Images != nil && *r.Images != f.HasImages {
		return false
	}
	if r.Tools != nil && *r.Tools != f.HasTools {
		return false
	}
	if r.Stream != nil && *r.Stream != f.Stream {
		return false
	}
	if r.MinInputTokens > 0 && f.InputTokens < r.MinInputTokens {
		return false
	}
	if r.MaxInputTokens > 0 && f.InputTokens > r.MaxInputTokens {
		return false
	}
	return true
}

func describeRule(r Rule) string {
	parts := []string{"use=" + r.Use}
	if r.Effort != "" {
		parts = append(parts, "effort="+r.Effort)
	}
	if r.Images != nil {
		parts = append(parts, fmt.Sprintf("images=%v", *r.Images))
	}
	if r.Tools != nil {
		parts = append(parts, fmt.Sprintf("tools=%v", *r.Tools))
	}
	if r.Stream != nil {
		parts = append(parts, fmt.Sprintf("stream=%v", *r.Stream))
	}
	if r.MinInputTokens > 0 {
		parts = append(parts, fmt.Sprintf("min_input_tokens=%d", r.MinInputTokens))
	}
	if r.MaxInputTokens > 0 {
		parts = append(parts, fmt.Sprintf("max_input_tokens=%d", r.MaxInputTokens))
	}
	return strings.Join(parts, " ")
}

func pinFirst(members []string, use string) []string {
	if use == "" {
		return members
	}
	out := make([]string, 0, len(members)+1)
	out = append(out, use)
	for _, m := range members {
		if m != use {
			out = append(out, m)
		}
	}
	return out
}

// ---------- 成员表达式匹配 ----------

// ResolveOrAuto 是网关实际使用的入口：先按名字找分组；找不到时再尝试
// 「零配置自动分组」的命名 `auto/model/<真实模型id>`。
//
// 这样调用方可以继续用裸模型名（liquid/lfm-3-1.7b）而不必知道分组名，
// 自动派生出的分组因此能透明生效 —— 这正是「用户不填模型、系统自动成组」的落点。
func (c *Catalog) ResolveOrAuto(name string, feat Features) (Selection, bool, error) {
	if _, ok := c.Get(name); ok {
		sel, err := c.Resolve(name, feat)
		return sel, true, err
	}
	auto := "auto/model/" + name
	if _, ok := c.Get(auto); ok {
		sel, err := c.Resolve(auto, feat)
		return sel, true, err
	}
	return Selection{}, false, nil
}

// MatchMember 判断一个成员表达式是否匹配某个真实候选。
//
//	channelID   实际渠道 id（如 chan_ab12cd…）
//	providerType 渠道类型（openai / anthropic / xai / gemini / openai_compatible）
//	modelID     上游真实模型 id（含斜杠，如 deepseek/deepseek-v4.1-flash）
//	alias       渠道内该模型的别名
//
// 表达式形如 `<channel|provider|*>/<model-glob>`；两侧都支持 `*` 通配。
func MatchMember(expr, channelID, providerType, modelID, alias string) bool {
	i := strings.IndexByte(expr, '/')
	if i < 0 {
		// 无斜杠：只按模型 id / 别名的后缀匹配（宽松写法）。
		return globMatch(expr, modelID) || globMatch(expr, alias)
	}
	left, right := expr[:i], expr[i+1:]
	leftOK := left == "*" || left == channelID || strings.EqualFold(left, providerType)
	if !leftOK {
		return false
	}
	return globMatch(right, modelID) || globMatch(right, alias) ||
		strings.HasSuffix(modelID, "/"+strings.TrimPrefix(right, "*/"))
}

// globMatch 支持 `*` 的简单通配（大小写不敏感，按字节匹配）。
// 不引入正则：表达式来自运维配置，保持可预测、无回溯风险。
func globMatch(pattern, s string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	p, t := strings.ToLower(pattern), strings.ToLower(s)
	if !strings.Contains(p, "*") {
		return p == t
	}
	parts := strings.Split(p, "*")
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(t[pos:], part)
		if idx < 0 {
			return false
		}
		if i == 0 && idx != 0 {
			return false // 首个片段必须贴头
		}
		pos += idx + len(part)
	}
	if last := parts[len(parts)-1]; last != "" {
		return strings.HasSuffix(t, last)
	}
	return true
}

// ---------- 持久化 ----------

// Load 从 <dir>/groups.json 载入分组目录。文件不存在时返回空目录（非错误）。
func Load(dir string) (*Catalog, error) {
	b, ok, err := fsutil.ReadFileIfExists(filepath.Join(dir, "groups.json"))
	if err != nil {
		return nil, err
	}
	if !ok {
		return NewCatalog(), nil
	}
	var c Catalog
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, errors.New("grouping: groups.json 解析失败: " + err.Error())
	}
	if !strings.EqualFold(c.Format, "tianheng-groups") {
		return nil, errors.New("grouping: groups.json 格式标识不符，拒绝载入")
	}
	return &c, nil
}

// Save 原子落盘到 <dir>/groups.json（0600，目录 0700），返回 SHA-256。
func (c *Catalog) Save(dir string) (string, error) {
	if c.Format == "" {
		c.Format = "tianheng-groups"
	}
	if c.Version == 0 {
		c.Version = 1
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	return fsutil.AtomicWriteFile(filepath.Join(dir, "groups.json"), append(b, '\n'))
}

// AutoDerive 从真实渠道模型派生一个分组：把同一真实上游模型 id 的所有
// (渠道, 上游模型) 组合聚成一个逻辑分组。这是天衡「零配置也能用」的关键：
// 用户不建分组时，系统按真实模型 id 自动成组。
//
// 返回分组数量。已存在的分组不被覆盖。
func (c *Catalog) AutoDerive(rows []Row) int {
	byModel := map[string][]string{}
	for _, r := range rows {
		if r.UpstreamModelID == "" || r.ChannelID == "" {
			continue
		}
		byModel[r.UpstreamModelID] = append(byModel[r.UpstreamModelID], r.ChannelID+"/"+r.UpstreamModelID)
	}
	n := 0
	for modelID, members := range byModel {
		ref := "auto/model/" + modelID
		if _, exists := c.Get(ref); exists {
			continue
		}
		sort.Strings(members)
		c.Add(Group{
			ID:      "auto-" + strings.NewReplacer("/", "-", ".", "-").Replace(modelID),
			Ref:     ref,
			Name:    modelID,
			Members: members,
		})
		n++
	}
	return n
}

// Row 是一条真实渠道-模型关系（来自 store），供 AutoDerive 使用。
type Row struct {
	ChannelID       string
	ProviderType    string
	UpstreamModelID string
	Alias           string
}
