// Package redact 原生实现「三级出站脱敏」原理。
//
// ── 原理来源 ────────────────────────────────────────────────────────────────
// 取自 AI 供应商聚合层 Magpie（com.yetone.magpie）。它在每次转发前对出站内容做
// 脱敏，并在路由决策里留下真实统计：
//
//	masked: { n: 707, distinct: 43,
//	          kinds: {API_KEY:2, EMAIL:4, PASSWORD:5, PERSON:1, SECRET:30, USERNAME:1},
//	          by:    {known:5, model:14, rule:24},
//	          key:   "179406..." }
//
// 由此提炼出三条原理：
//  1. **三级来源（by）**：
//       known —— 系统已知的确定值（渠道密钥、令牌、账号、邮箱）精确匹配，优先级最高；
//       model —— 结构化识别器（原语为可插拔识别器，天衡离线默认用本地结构识别）；
//       rule  —— 确定性规则（前缀/形状/上下文）匹配。
//  2. **按种类计数（kinds）**：API_KEY / SECRET / PASSWORD / EMAIL / PERSON /
//     USERNAME / PHONE / IP / HOME / BUCKET / USERHOST，逐类计数便于审计与告警。
//  3. **可逆**：出站把敏感串替换成占位符，模型若在回答里引用该占位符，入站时必须
//     还原成原值 —— 否则用户看到的是自己的密钥被换成了乱码。
//
// ── 为什么 LLM 网关必须有这个 ───────────────────────────────────────────────
// 天衡会把用户的对话原样转发给第三方上游。用户的 prompt 里经常夹带真实密钥、
// 内网地址、家目录、云存储桶名。这些一旦进入上游日志就无法收回。出站脱敏把
// 「确定不该外泄的字符串」替换掉，再由入站还原保证功能不受影响。
//
// ── 诚实标注 ────────────────────────────────────────────────────────────────
// tier=model 的原语在原理来源里是一个独立的守卫模型。天衡离线可用、且要求零第三方
// 依赖，因此默认实现是**本地结构识别器**（确定性代码，不调用任何外部模型），
// 其名称与能力通过 Policy.ModelName / Policy.ModelAuto 如实暴露，绝不假装是 LLM。
// 需要更强识别时可注入自定义 Recognizer（接口已留出）。
package redact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"llm-hub/src/internal/fsutil"
	"path/filepath"
)

// 脱敏种类（与原理来源的 kinds 对齐，并补齐网关实际会遇到的类别）。
const (
	KindAPIKey   = "API_KEY"
	KindSecret   = "SECRET"
	KindPassword = "PASSWORD"
	KindEmail    = "EMAIL"
	KindPerson   = "PERSON"
	KindUsername = "USERNAME"
	KindPhone    = "PHONE"
	KindIP       = "IP"
	KindHome     = "HOME"
	KindBucket   = "BUCKET"
	KindUserHost = "USERHOST"
)

// 三级来源。
const (
	TierKnown = "known"
	TierModel = "model"
	TierRule  = "rule"
)

// AllKinds 是稳定的种类顺序（审计输出不抖动）。
var AllKinds = []string{
	KindAPIKey, KindSecret, KindPassword, KindEmail, KindPerson,
	KindUsername, KindPhone, KindIP, KindHome, KindBucket, KindUserHost,
}

// Policy 是脱敏策略。
type Policy struct {
	Enabled     bool              `json:"enabled"`
	WholeWords  bool              `json:"whole_words"`   // 仅当匹配两侧是非单词字符时才替换
	ModelName   string            `json:"model_name"`    // tier=model 的识别器名称（如实标注）
	ModelAuto   bool              `json:"model_auto"`    // 是否启用该识别器
	Personal    bool              `json:"personal"`      // 是否启用 PERSON/PHONE 等个人标识识别
	Kinds       map[string]bool   `json:"kinds"`         // 逐类开关；缺省视为 true
	ExtraWords  []string          `json:"extra_words"`   // 额外需要整体屏蔽的词（配置项）
	MaskPrefix  string            `json:"mask_prefix"`   // 占位符前缀
	Extensions  map[string]string `json:"extensions"`    // 预留：自定义种类名 → 说明
}

// DefaultPolicy 返回生产默认策略：开启脱敏、整词匹配、启用结构识别、开启个人标识。
func DefaultPolicy() Policy {
	return Policy{
		Enabled:    true,
		WholeWords: true,
		ModelName:  "local-structural-guard", // 如实标注：本地结构识别器，非 LLM
		ModelAuto:  true,
		Personal:   true,
		Kinds:      map[string]bool{},
		MaskPrefix: "«",
	}
}

func (p Policy) kindEnabled(k string) bool {
	if p.Kinds == nil {
		return true
	}
	v, ok := p.Kinds[k]
	if !ok {
		return true // 未显式配置 = 开启
	}
	return v
}

// Recognizer 是 tier=model 的可插拔识别器。
// 返回命中的 (起始下标, 结束下标, 种类)；实现必须是纯函数、可重入。
type Recognizer interface {
	Name() string
	Find(text string) []Span
}

// Span 是一段被识别出的敏感区间。
type Span struct {
	Start int
	End   int
	Kind  string
}

// Report 是一次脱敏的真实统计，字段与原理来源一一对应。
type Report struct {
	N        int            `json:"n"`        // 替换的占用数（同一原值多次出现分别计数）
	Distinct int            `json:"distinct"` // 去重后的不同原值个数
	Kinds    map[string]int `json:"kinds"`    // 逐类计数
	By       map[string]int `json:"by"`       // 逐级计数：known|model|rule
	Key      string         `json:"key"`      // 请求级关联键（便于与路由日志串起）
}

// Empty 判断本次是否未发生任何替换。
func (r Report) Empty() bool { return r.N == 0 }

// Masker 持有一次会话内稳定的脱敏映射。
// 注意：映射是 per-Masker 的，Restore 只能还原本 Masker 产生的占位符 ——
// 这正是需要的行为（避免把别的请求的占位符错误还原成本请求的密钥）。
type Masker struct {
	pol        Policy
	recognizer Recognizer

	mu      sync.Mutex
	forward map[string]string // 原值 -> 占位符
	reverse map[string]string // 占位符 -> 原值
	seq     map[string]int    // 种类 -> 已分配编号
	known   []string          // tier=known：系统已知值（按长度降序，先长后短免得子串抢先）
	key     string
}

// NewMasker 构造一个脱敏器。pol.MaskPrefix 为空时使用 "«"。
func NewMasker(pol Policy) *Masker {
	if pol.MaskPrefix == "" {
		pol.MaskPrefix = "«"
	}
	m := &Masker{
		pol:        pol,
		recognizer: NewStructuralGuard(),
		forward:    map[string]string{},
		reverse:    map[string]string{},
		seq:        map[string]int{},
	}
	return m
}

// SetRecognizer 替换 tier=model 的识别器。
func (m *Masker) SetRecognizer(r Recognizer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r != nil {
		m.recognizer = r
	}
}

// RegisterKnown 登记 tier=known 的确定值（渠道 api_key、平台令牌明文、
// 用户邮箱、账号标识等）。空串与过短值（<4 字节）会被忽略：太短的值当整词匹配
// 会误伤大量正常文本。
func (m *Masker) RegisterKnown(values ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range values {
		v = strings.TrimSpace(v)
		if len(v) < 4 {
			continue
		}
		if containsStr(m.known, v) {
			continue
		}
		m.known = append(m.known, v)
	}
	// 长的先匹配，"sk-abcdef" 不应被 "sk-abc" 抢先切成两段。
	sort.SliceStable(m.known, func(i, j int) bool { return len(m.known[i]) > len(m.known[j]) })
}

// SetKey 设置请求级关联键（用于把脱敏统计与被脱敏的那次调用串起来）。
func (m *Masker) SetKey(k string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.key = k
}

// Key 返回关联键。
func (m *Masker) Key() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.key
}

// Mask 对出站文本做三级脱敏，返回替换后的文本与真实统计。
// 未启用策略时原样返回且 Report 为零值（如实反映「没做事」）。
func (m *Masker) Mask(text string) (string, Report) {
	rep := Report{Kinds: map[string]int{}, By: map[string]int{}, Key: m.Key()}
	if !m.pol.Enabled || text == "" {
		return text, rep
	}

	type hit struct {
		start, end int
		kind, tier string
		value      string
	}
	var hits []hit

	m.mu.Lock()
	known := append([]string(nil), m.known...)
	rec := m.recognizer
	m.mu.Unlock()

	// ---- tier=known：已知值精确匹配（最高优先级，最先占位）----
	for _, v := range known {
		for idx := 0; ; {
			i := strings.Index(text[idx:], v)
			if i < 0 {
				break
			}
			abs := idx + i
			if !m.bounded(text, abs, abs+len(v)) {
				idx = abs + 1
				continue
			}
			hits = append(hits, hit{abs, abs + len(v), kindOfKnown(v), TierKnown, v})
			idx = abs + len(v)
		}
	}

	// ---- tier=model：结构化识别器（本地实现，可插拔）----
	if m.pol.ModelAuto && rec != nil {
		for _, sp := range rec.Find(text) {
			if sp.Start < 0 || sp.End > len(text) || sp.Start >= sp.End {
				continue
			}
			if !m.pol.kindEnabled(sp.Kind) {
				continue
			}
			hits = append(hits, hit{sp.Start, sp.End, sp.Kind, TierModel, text[sp.Start:sp.End]})
		}
	}

	// ---- tier=rule：确定性规则 ----
	for _, r := range ruleSet {
		if !m.pol.kindEnabled(r.kind) {
			continue
		}
		for _, loc := range r.re.FindAllStringIndex(text, -1) {
			if !m.bounded(text, loc[0], loc[1]) {
				continue
			}
			hits = append(hits, hit{loc[0], loc[1], r.kind, TierRule, text[loc[0]:loc[1]]})
		}
	}

	if len(hits) == 0 {
		return text, rep
	}

	// 重叠消解：先按起点、再按「known > model > rule」、最后按长度降序，
	// 保证已知值优先、外层区间优先，绝不产生交叉替换。
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].start != hits[j].start {
			return hits[i].start < hits[j].start
		}
		pi, pj := tierRank(hits[i].tier), tierRank(hits[j].tier)
		if pi != pj {
			return pi < pj
		}
		return hits[i].end > hits[j].end
	})

	var b strings.Builder
	b.Grow(len(text))
	last := 0
	seen := map[string]bool{}
	for _, h := range hits {
		if h.start < last {
			continue // 与已接受的替换重叠
		}
		b.WriteString(text[last:h.start])
		ph := m.placeholder(h.kind, h.value)
		b.WriteString(ph)
		last = h.end

		rep.N++
		rep.Kinds[h.kind]++
		rep.By[h.tier]++
		if !seen[h.value] {
			seen[h.value] = true
			rep.Distinct++
		}
	}
	b.WriteString(text[last:])
	return b.String(), rep
}

// Restore 把本 Masker 产生的占位符还原回原值（入站方向）。
// 只还原自己产生的占位符，不会误伤其他请求的内容。
func (m *Masker) Restore(text string) string {
	if text == "" {
		return text
	}
	m.mu.Lock()
	rev := make(map[string]string, len(m.reverse))
	for k, v := range m.reverse {
		rev[k] = v
	}
	m.mu.Unlock()
	if len(rev) == 0 {
		return text
	}
	// 占位符可能很多：单次遍历做替换（避免 N 次全串扫描）。
	var b strings.Builder
	b.Grow(len(text))
	i := 0
	for i < len(text) {
		matched := false
		if strings.HasPrefix(text[i:], m.pol.MaskPrefix) {
			if j := strings.Index(text[i:], "»"); j > 0 {
				cand := text[i : i+j+len("»")]
				if orig, ok := rev[cand]; ok {
					b.WriteString(orig)
					i += len(cand)
					matched = true
				}
			}
		}
		if !matched {
			b.WriteByte(text[i])
			i++
		}
	}
	return b.String()
}

// Stats 返回当前映射规模（供面板显示）。
func (m *Masker) Stats() (distinct int, byKind map[string]int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	byKind = map[string]int{}
	for _, ph := range m.forward {
		byKind[kindFromPlaceholder(ph)]++
	}
	return len(m.forward), byKind
}

// ---------- 内部 ----------

// placeholder 返回某原值对应的占位符；同一原值在整个 Masker 生命周期内稳定。
func (m *Masker) placeholder(kind, value string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ph, ok := m.forward[value]; ok {
		return ph
	}
	m.seq[kind]++
	ph := fmt.Sprintf("%s%s#%d»", m.pol.MaskPrefix, kind, m.seq[kind])
	m.forward[value] = ph
	m.reverse[ph] = value
	return ph
}

func kindFromPlaceholder(ph string) string {
	s := strings.TrimPrefix(ph, "«")
	if i := strings.IndexByte(s, '#'); i > 0 {
		return s[:i]
	}
	return "UNKNOWN"
}

// bounded 判断 [s,e) 是否落在「整词」边界内（对应 redactWordsWhole）。
//
// ── 关键修正（实测缺陷）────────────────────────────────────────────────────
// 第一版把「任何 >= 0x80 的字节」都当成单词字符。在中文文本里这会导致灾难性
// 后果：`服务器 10.20.30.40，` 这种句子里，IP 后面的「，」是 3 字节 UTF-8
// （0xEF 0xBC 0x8C），被判成单词字符 → 边界不成立 → **整条脱敏规则在中文语境
// 下完全失效**。中文技术文档里几乎每个敏感串后面都跟着汉字或中文标点，等于
// 脱敏形同虚设。
//
// 正确做法：边界判定取决于**被匹配值自身属于哪种书写系统**。
//   · 纯 ASCII 值（密钥、IP、邮箱、路径）—— 只要两侧不是 ASCII 单词字符即可，
//     汉字与中文标点都算合法边界（中文不用空格分词，不能强求空格）。
//   · 含 CJK 的值（中文人名等）—— 两侧还须不是汉字，否则「张伟同学」
//     里的姓名会被当作更长名字的一部分。
func (m *Masker) bounded(text string, s, e int) bool {
	if !m.pol.WholeWords {
		return true
	}
	cjk := hasCJK(text[s:e])
	if s > 0 {
		if isASCIIWord(text[s-1]) {
			return false
		}
		if cjk {
			if r, size := lastRune(text[:s]); size > 0 && isCJKRune(r) {
				return false
			}
		}
	}
	if e < len(text) {
		if isASCIIWord(text[e]) {
			return false
		}
		if cjk {
			if r, size := firstRune(text[e:]); size > 0 && isCJKRune(r) {
				return false
			}
		}
	}
	return true
}

// isASCIIWord 判断是否 ASCII 单词字符（下划线/数字/字母）。
func isASCIIWord(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isCJKRune(r rune) bool {
	return (r >= 0x3000 && r <= 0x303F) || // CJK 标点
		(r >= 0x3400 && r <= 0x4DBF) || // 扩展 A
		(r >= 0x4E00 && r <= 0x9FFF) || // 基本区
		(r >= 0xF900 && r <= 0xFAFF) || // 兼容表意
		(r >= 0xFF00 && r <= 0xFFEF) // 全角形式
}

func hasCJK(s string) bool {
	for _, r := range s {
		if isCJKRune(r) && r < 0xFF00 { // 全角标点不算"含中文"
			return true
		}
	}
	return false
}

func lastRune(s string) (rune, int) {
	if s == "" {
		return 0, 0
	}
	for i := len(s) - 1; i >= 0; i-- {
		if utf8.RuneStart(s[i]) {
			r, size := utf8.DecodeRuneInString(s[i:])
			return r, size
		}
	}
	return 0, 0
}

func firstRune(s string) (rune, int) {
	if s == "" {
		return 0, 0
	}
	r, size := utf8.DecodeRuneInString(s)
	return r, size
}

// kindOfKnown 依据已知值自身的形状给它归类（用于统计，不影响是否替换）。
func kindOfKnown(v string) string {
	switch {
	case strings.HasPrefix(v, "sk-"), strings.HasPrefix(v, "ghp_"), strings.HasPrefix(v, "xoxb-"),
		strings.HasPrefix(v, "AKIA"), strings.HasPrefix(v, "AIza"):
		return KindAPIKey
	case strings.Contains(v, "@") && strings.Contains(v, "."):
		return KindEmail
	case strings.HasPrefix(v, "/Users/"), strings.HasPrefix(v, "/home/"), strings.HasPrefix(v, "~/"):
		return KindHome
	default:
		return KindSecret
	}
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func tierRank(t string) int {
	switch t {
	case TierKnown:
		return 0
	case TierModel:
		return 1
	default:
		return 2
	}
}

// ---------- tier=rule 的确定性规则集 ----------

type rule struct {
	kind string
	re   *regexp.Regexp
}

var ruleSet = []rule{
	// 常见厂商密钥前缀：命中即整体屏蔽。
	{KindAPIKey, regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_\-]{12,}\b`)},
	{KindAPIKey, regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`)},
	{KindAPIKey, regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9\-]{10,}\b`)},
	{KindAPIKey, regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{KindAPIKey, regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`)},
	{KindAPIKey, regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{6,}\b`)}, // JWT

	// 上下文式：必须在赋值语境里才屏蔽，降低误伤。
	{KindPassword, regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|secret)\s*[:=]\s*["']?([^\s"',;]{6,})`)},
	{KindAPIKey, regexp.MustCompile(`(?i)\b(?:api[_-]?key|apikey|access[_-]?token|auth[_-]?token)\s*[:=]\s*["']?([^\s"',;]{8,})`)},

	// 网络与存储位置。
	{KindIP, regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)},
	{KindBucket, regexp.MustCompile(`\b(?:s3|gs)://[A-Za-z0-9._\-]{3,}`)},
	{KindBucket, regexp.MustCompile(`\b[A-Za-z0-9._\-]{2,}\.s3\.[a-z0-9\-]+\.amazonaws\.com\b`)},
	{KindHome, regexp.MustCompile(`(?:/Users/|/home/)[A-Za-z0-9._\-]+(?:/[A-Za-z0-9._\-]+)*`)},
	// EMAIL 必须排在 USERHOST 之前：USERHOST 更宽松，若先匹配会把邮箱
	// 吞成 user@domain 而归类错误（实测缺陷：jiuri@example.com → USERHOST）。
	{KindEmail, regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)},
	{KindUserHost, regexp.MustCompile(`\b[A-Za-z0-9._\-]{1,32}@[A-Za-z0-9._\-]{3,}\.(?:com|net|org|io|cn|dev|ai|co)\b`)},
}

// ---------- tier=model 的本地结构识别器 ----------

// StructuralGuard 是天衡默认的结构识别器（tier=model）。
//
// ── 为什么不做「姓氏 + 1~2 字」的朴素人名识别 ──────────────────────────────
// 实测结论（本包测试输出）：朴素姓氏规则在中文里误判率极高 —— 「路径」「后缀」
// 「家目录」都会被判成人名，结果是**把用户正常的请求内容替换成占位符**，
// 破坏语义。这类"看起来很厉害但实际有害"的启发式必须放弃。
//
// 因此本实现只做两类**高置信度**识别：
//   1. 手机号：形状固定（1[3-9] + 9 位），误判率极低；
//   2. 人名：**必须有上下文锚点**（我叫 / 姓名: / name: / Mr. / Dr. 等），
//      只遮蔽锚点后面的那一小段，不主动扫描全篇猜名字。
//
// 需要全篇通用 NER 时，通过 SetRecognizer 注入真正的识别器 —— 接口已留出，
// 本类型不假装自己是 LLM。
type StructuralGuard struct {
	phoneRe  *regexp.Regexp
	cnNameRe *regexp.Regexp // 捕获组 1 才是人名本体
	enNameRe *regexp.Regexp // 捕获组 1
}

// NewStructuralGuard 构造默认识别器。
func NewStructuralGuard() *StructuralGuard {
	return &StructuralGuard{
		// 中国大陆手机号（含 +86 前缀与常见分隔符）
		phoneRe: regexp.MustCompile(`(?:\+?86[-\s]?)?\b1[3-9]\d{9}\b`),
		// 中文人名：必须紧跟显式锚点，抓锚点后的 2~4 个汉字
		cnNameRe: regexp.MustCompile(`(?:我叫|我是|姓名|名字|称呼|联系人|负责人|name|Name)\s*[:：]?\s*([\p{Han}]{2,4})`),
		// 英文人名：必须带称谓前缀
		enNameRe: regexp.MustCompile(`\b(?:Mr|Ms|Mrs|Dr|Prof)\.?\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)`),
	}
}

// Name 返回识别器名称（如实标注为本地实现）。
func (g *StructuralGuard) Name() string { return "local-structural-guard" }

// Find 返回识别到的敏感区间。只返回捕获组 1（人名本体）的范围，
// 不遮蔽锚点词本身，避免把「我叫」也替换掉。
func (g *StructuralGuard) Find(text string) []Span {
	out := []Span{}
	for _, loc := range g.phoneRe.FindAllStringIndex(text, -1) {
		out = append(out, Span{Start: loc[0], End: loc[1], Kind: KindPhone})
	}
	for _, re := range []*regexp.Regexp{g.cnNameRe, g.enNameRe} {
		for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
			// m[2],m[3] = 第 1 个捕获组的 [start,end)
			if len(m) >= 4 && m[2] >= 0 && m[3] > m[2] {
				out = append(out, Span{Start: m[2], End: m[3], Kind: KindPerson})
			}
		}
	}
	return out
}

// ---------- 审计日志（按日分片，与原理来源的 masking/YYYY-MM-DD.jsonl 一致）----------

// AuditRecord 是一条脱敏审计记录。
type AuditRecord struct {
	Time       string `json:"time"`
	Key        string `json:"key"`
	Conversation string `json:"conv,omitempty"`
	Group      string `json:"group,omitempty"`
	Channel    string `json:"channel,omitempty"`
	N          int    `json:"n"`
	Distinct   int    `json:"distinct"`
	Kinds      map[string]int `json:"kinds"`
	By         map[string]int `json:"by"`
	Bytes      int    `json:"bytes"` // 被替换文本的总字节数（用于评估外泄面）
}

// Journal 按日分片写脱敏审计日志到 <dir>/masking/YYYY-MM-DD.jsonl。
// 绝不写入原值 —— 只写计数与被替换文本的长度。
type Journal struct {
	dir string
	mu  sync.Mutex
}

// NewJournal 构造审计日志。
func NewJournal(dataDir string) *Journal {
	return &Journal{dir: filepath.Join(dataDir, "masking")}
}

// Append 追加一条审计记录。
func (j *Journal) Append(rec AuditRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	day := rec.Time
	if len(day) >= 10 {
		day = day[:10]
	} else {
		day = "unknown"
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return fsutil.AppendLine(filepath.Join(j.dir, day+".jsonl"), b)
}

// Hash 计算某个值的稳定指纹（用于审计时不落原文）。
func Hash(v string) string {
	s := sha256.Sum256([]byte(v))
	return hex.EncodeToString(s[:8])
}
