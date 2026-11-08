// Package forum 实现「专门用于交流的社区论坛」：版块 Board / 帖子 Post /
// 回复 Reply / 点赞 Like，以及置顶、检索、分页。
//
// 硬约束：
//   - 零第三方 Go 依赖（只用标准库 + 项目内已有包）。
//   - 不改 src/internal/store/：论坛数据持久化在独立 JSON 文件
//     <cfg.DataDir>/forum.json（DataDir 为空时回退 ./.llmhub-data，与 cmd/server/main.go 一致）。
//   - 禁止任何种子 / 演示 / 假数据：初始 boards/posts/replies/likes 全为空数组；
//     版块由「真实发帖提交」创建，帖子/回复/点赞/时间全部来自真实请求。
//   - 输出做 XSS 防护：用户输入经 sanitizeText 规范化为纯文本，响应体不含原始 HTML；
//     Go json.Encoder 默认转义 < > &，前端以 React 文本节点渲染，绝不 dangerouslySetInnerHTML。
package forum

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// 长度上限（按 rune 计，避免截断 UTF-8 半个字符）。
const (
	maxTitleRunes       = 200
	maxContentRunes     = 20000
	maxBoardSlugRunes   = 32
	maxBoardNameRunes   = 60
	maxBoardDescRunes   = 200
	maxQueryRunes       = 100
	defaultPageSize     = 20
	maxPageSize         = 50
	maxPage             = 1000000
	maxExcerptRunes     = 160
)

// Board 版块。版块不是内置常量：首次向某个 slug 真实发帖时才落库。
type Board struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   int64  `json:"created_at"`
}

// Post 帖子。LikeCount / ReplyCount 由真实 Like / Reply 记录派生（启动重算），
// 不允许手工填写。
type Post struct {
	ID         string `json:"id"`
	BoardID    string `json:"board_id"`
	AuthorID   string `json:"author_id"`
	AuthorName string `json:"author_name"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	Pinned     bool   `json:"pinned"`
	LikeCount  int    `json:"like_count"`
	ReplyCount int    `json:"reply_count"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// Reply 回复。
type Reply struct {
	ID         string `json:"id"`
	PostID     string `json:"post_id"`
	AuthorID   string `json:"author_id"`
	AuthorName string `json:"author_name"`
	Content    string `json:"content"`
	CreatedAt  int64  `json:"created_at"`
}

// Like 点赞。同一 (post_id, user_id) 只允许一条，保证 like_count 真实。
type Like struct {
	ID        string `json:"id"`
	PostID    string `json:"post_id"`
	UserID    string `json:"user_id"`
	CreatedAt int64  `json:"created_at"`
}

// database 是 forum.json 的完整内容。切片元素为指针，便于锁内原地更新。
type database struct {
	Boards  []*Board `json:"boards"`
	Posts   []*Post  `json:"posts"`
	Replies []*Reply `json:"replies"`
	Likes   []*Like  `json:"likes"`
}

func newDatabase() database {
	return database{
		Boards:  []*Board{},
		Posts:   []*Post{},
		Replies: []*Reply{},
		Likes:   []*Like{},
	}
}

var (
	// htmlTagRe 剥离成对的 HTML/XML 标签（含跨行）。
	htmlTagRe = regexp.MustCompile(`(?s)<[^>]*>`)
	// angleReplacer 把残余尖括号替换为书名号字符，保证结果不可能构成 HTML 标签。
	angleReplacer = strings.NewReplacer("<", "‹", ">", "›")
	// boardSlugRe 版块 slug 只允许小写字母/数字/连字符，禁止路径穿越与 HTML。
	boardSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	// entityIDRe 校验 PathValue 里的 id，防止超长/异常字符进入日志与文件。
	entityIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// sanitizeText 把用户输入规范化为纯文本：
//  1. 去 NUL；
//  2. 剥离所有 HTML 标签（<script>alert(1)</script> -> alert(1)）；
//  3. 把残余 '<' '>' 替换为 ‹ ›，使输出字符串不可能含原始 HTML 标签；
//  4. 去掉控制字符（保留 \n \r \t）；
//  5. TrimSpace + 按 rune 截断到 maxRunes（<=0 表示不截断）。
//
// 这是 XSS 防护第一层（数据层不回显原始 HTML）；第二层是 Go json.Encoder
// 默认 SetEscapeHTML(true)；第三层是前端 React 文本节点渲染。
func sanitizeText(s string, maxRunes int) string {
	s = strings.ReplaceAll(s, "\x00", "")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = angleReplacer.Replace(s)
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if maxRunes > 0 && utf8.RuneCountInString(s) > maxRunes {
		s = strings.TrimSpace(string([]rune(s)[:maxRunes]))
	}
	return s
}

// excerpt 生成列表用的纯文本摘要（内容本身已 sanitize）。
func excerpt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= maxExcerptRunes {
		return s
	}
	return string([]rune(s)[:maxExcerptRunes]) + "…"
}

// ---------- 对外响应视图（全部为纯文本，绝不含原始 HTML） ----------

// BoardView 版块视图，post_count 由真实帖子派生。
type BoardView struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	PostCount   int    `json:"post_count"`
	CreatedAt   int64  `json:"created_at"`
}

// PostView 帖子视图。Liked 反映当前请求用户是否已点赞（未登录恒为 false）。
type PostView struct {
	ID         string `json:"id"`
	BoardID    string `json:"board_id"`
	BoardSlug  string `json:"board_slug"`
	AuthorID   string `json:"author_id"`
	AuthorName string `json:"author_name"`
	Title      string `json:"title"`
	Excerpt    string `json:"excerpt"`
	Content    string `json:"content,omitempty"`
	Pinned     bool   `json:"pinned"`
	LikeCount  int    `json:"like_count"`
	ReplyCount int    `json:"reply_count"`
	Liked      bool   `json:"liked"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// ReplyView 回复视图。
type ReplyView struct {
	ID         string `json:"id"`
	PostID     string `json:"post_id"`
	AuthorID   string `json:"author_id"`
	AuthorName string `json:"author_name"`
	Content    string `json:"content"`
	CreatedAt  int64  `json:"created_at"`
}

// BoardList 版块列表响应。
type BoardList struct {
	Items []BoardView `json:"items"`
	Total int         `json:"total"`
}

// PostList 帖子分页列表响应。
type PostList struct {
	Items    []PostView `json:"items"`
	Total    int        `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Query    string     `json:"query"`
	Board    string     `json:"board"`
}

// PostDetail 帖子详情响应。
type PostDetail struct {
	Post    PostView    `json:"post"`
	Replies []ReplyView `json:"replies"`
}

// LikeResult 点赞响应。
type LikeResult struct {
	PostID    string `json:"post_id"`
	LikeCount int    `json:"like_count"`
	Liked     bool   `json:"liked"`
}

// ActionResult 通用动作响应。
type ActionResult struct {
	OK bool   `json:"ok"`
	ID string `json:"id,omitempty"`
}

// ---------- 请求体 ----------

type createPostRequest struct {
	Board            string `json:"board"`
	BoardName        string `json:"board_name"`
	BoardDescription string `json:"board_description"`
	Title            string `json:"title"`
	Content          string `json:"content"`
}

type createReplyRequest struct {
	Content string `json:"content"`
}

type pinRequest struct {
	Pinned *bool `json:"pinned"`
}
