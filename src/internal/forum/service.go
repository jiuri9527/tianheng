package forum

import (
	"errors"
	"sort"
	"strings"
	"time"

	"llm-hub/src/internal/store"
)

var (
	errBoardRequired   = errors.New("版块 slug 不能为空")
	errBoardInvalid    = errors.New("版块 slug 不合法：只允许小写字母/数字/连字符，长度 1-32")
	errTitleRequired   = errors.New("标题不能为空")
	errContentRequired = errors.New("内容不能为空")
	errPostNotFound    = errors.New("帖子不存在")
	errForbidden       = errors.New("无权操作该帖子")
	errNotAuthor       = errors.New("仅帖子作者可执行该操作（天衡无管理员角色）")
	errReplyNotFound   = errors.New("回复不存在")
)

func nowUnix() int64 { return time.Now().Unix() }

// displayName 从真实 store.User 派生显示名：取邮箱本地部分（@ 前），
// 派生失败时回退到真实用户 ID。绝不编造用户名。
func displayName(u *store.User) string {
	if u == nil {
		return ""
	}
	e := strings.TrimSpace(u.Email)
	if i := strings.IndexByte(e, '@'); i > 0 {
		e = e[:i]
	}
	e = sanitizeText(e, 40)
	if e == "" {
		e = u.ID
	}
	return e
}

func findBoardBySlug(db *database, slug string) *Board {
	for _, b := range db.Boards {
		if b != nil && b.Slug == slug {
			return b
		}
	}
	return nil
}

func boardSlugs(db *database) map[string]string {
	out := make(map[string]string, len(db.Boards))
	for _, b := range db.Boards {
		if b != nil {
			out[b.ID] = b.Slug
		}
	}
	return out
}

func findPost(db *database, id string) *Post {
	for _, p := range db.Posts {
		if p != nil && p.ID == id {
			return p
		}
	}
	return nil
}

// listBoards 返回真实版块列表；PostCount 由真实帖子派生。
// 初始（无任何真实提交）时 Items 为空数组、Total=0，即真实空态。
func (fs *fileStore) listBoards() BoardList {
	out := BoardList{Items: []BoardView{}}
	fs.read(func(db *database) {
		for _, b := range db.Boards {
			if b == nil {
				continue
			}
			n := 0
			for _, p := range db.Posts {
				if p != nil && p.BoardID == b.ID {
					n++
				}
			}
			out.Items = append(out.Items, BoardView{
				ID: b.ID, Slug: b.Slug, Name: b.Name,
				Description: b.Description, PostCount: n, CreatedAt: b.CreatedAt,
			})
		}
		out.Total = len(out.Items)
	})
	sort.Slice(out.Items, func(i, j int) bool {
		if out.Items[i].CreatedAt != out.Items[j].CreatedAt {
			return out.Items[i].CreatedAt < out.Items[j].CreatedAt
		}
		return out.Items[i].Slug < out.Items[j].Slug
	})
	return out
}

// listPosts 支持版块过滤、标题/正文检索、置顶优先、分页。
// q 先经 sanitizeText，绝不把原始查询串回显到响应（XSS）。
func (fs *fileStore) listPosts(boardSlug, q string, page, pageSize int, viewerID string) PostList {
	q = sanitizeText(q, maxQueryRunes)
	ql := strings.ToLower(q)
	boardSlug = strings.ToLower(strings.TrimSpace(boardSlug))
	if page < 1 {
		page = 1
	}
	if page > maxPage {
		page = maxPage
	}
	if pageSize < 1 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	result := PostList{
		Items: []PostView{}, Page: page, PageSize: pageSize,
		Query: q, Board: boardSlug,
	}
	fs.read(func(db *database) {
		liked := map[string]bool{}
		if viewerID != "" {
			for _, l := range db.Likes {
				if l != nil && l.UserID == viewerID {
					liked[l.PostID] = true
				}
			}
		}
		slugs := boardSlugs(db)
		matched := make([]*Post, 0, len(db.Posts))
		for _, p := range db.Posts {
			if p == nil {
				continue
			}
			if boardSlug != "" && slugs[p.BoardID] != boardSlug {
				continue
			}
			if ql != "" &&
				!strings.Contains(strings.ToLower(p.Title), ql) &&
				!strings.Contains(strings.ToLower(p.Content), ql) {
				continue
			}
			matched = append(matched, p)
		}
		// 置顶优先 -> 创建时间倒序 -> id 倒序（同秒稳定排序）。
		sort.Slice(matched, func(i, j int) bool {
			a, b := matched[i], matched[j]
			if a.Pinned != b.Pinned {
				return a.Pinned
			}
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt > b.CreatedAt
			}
			return a.ID > b.ID
		})
		result.Total = len(matched)
		start := (page - 1) * pageSize
		if start > result.Total {
			start = result.Total
		}
		end := start + pageSize
		if end > result.Total {
			end = result.Total
		}
		for _, p := range matched[start:end] {
			result.Items = append(result.Items, PostView{
				ID: p.ID, BoardID: p.BoardID, BoardSlug: slugs[p.BoardID],
				AuthorID: p.AuthorID, AuthorName: p.AuthorName,
				Title: p.Title, Excerpt: excerpt(p.Content),
				Pinned: p.Pinned, LikeCount: p.LikeCount, ReplyCount: p.ReplyCount,
				Liked: liked[p.ID], CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
			})
		}
	})
	return result
}

// createPost 由一次真实提交产生帖子；若该版块 slug 首次出现，同时由本次
// 提交的 board_name / board_description 创建版块（版块也来自真实提交，非种子）。
func (fs *fileStore) createPost(u *store.User, req createPostRequest) (PostView, error) {
	slug := strings.ToLower(strings.TrimSpace(sanitizeText(req.Board, maxBoardSlugRunes)))
	name := sanitizeText(req.BoardName, maxBoardNameRunes)
	desc := sanitizeText(req.BoardDescription, maxBoardDescRunes)
	title := sanitizeText(req.Title, maxTitleRunes)
	content := sanitizeText(req.Content, maxContentRunes)
	if slug == "" {
		return PostView{}, errBoardRequired
	}
	if !boardSlugRe.MatchString(slug) {
		return PostView{}, errBoardInvalid
	}
	if title == "" {
		return PostView{}, errTitleRequired
	}
	if content == "" {
		return PostView{}, errContentRequired
	}
	if name == "" {
		name = slug
	}
	now := nowUnix()
	var view PostView
	err := fs.write(func(db *database) error {
		b := findBoardBySlug(db, slug)
		if b == nil {
			b = &Board{
				ID: store.NewID("brd"), Slug: slug, Name: name,
				Description: desc, CreatedAt: now,
			}
			db.Boards = append(db.Boards, b)
		}
		p := &Post{
			ID: store.NewID("post"), BoardID: b.ID, AuthorID: u.ID,
			AuthorName: displayName(u), Title: title, Content: content,
			Pinned: false, LikeCount: 0, ReplyCount: 0,
			CreatedAt: now, UpdatedAt: now,
		}
		db.Posts = append(db.Posts, p)
		view = PostView{
			ID: p.ID, BoardID: p.BoardID, BoardSlug: b.Slug,
			AuthorID: p.AuthorID, AuthorName: p.AuthorName,
			Title: p.Title, Excerpt: excerpt(p.Content), Content: p.Content,
			Pinned: p.Pinned, LikeCount: p.LikeCount, ReplyCount: p.ReplyCount,
			Liked: false, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		}
		return nil
	})
	return view, err
}

// getPost 返回帖子详情与真实回复列表（按时间正序）。
func (fs *fileStore) getPost(id, viewerID string) (PostDetail, error) {
	detail := PostDetail{Replies: []ReplyView{}}
	found := false
	fs.read(func(db *database) {
		p := findPost(db, id)
		if p == nil {
			return
		}
		found = true
		liked := false
		if viewerID != "" {
			for _, l := range db.Likes {
				if l != nil && l.PostID == id && l.UserID == viewerID {
					liked = true
					break
				}
			}
		}
		slugs := boardSlugs(db)
		detail.Post = PostView{
			ID: p.ID, BoardID: p.BoardID, BoardSlug: slugs[p.BoardID],
			AuthorID: p.AuthorID, AuthorName: p.AuthorName,
			Title: p.Title, Excerpt: excerpt(p.Content), Content: p.Content,
			Pinned: p.Pinned, LikeCount: p.LikeCount, ReplyCount: p.ReplyCount,
			Liked: liked, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		}
		rs := make([]*Reply, 0)
		for _, r := range db.Replies {
			if r != nil && r.PostID == id {
				rs = append(rs, r)
			}
		}
		sort.Slice(rs, func(i, j int) bool {
			if rs[i].CreatedAt != rs[j].CreatedAt {
				return rs[i].CreatedAt < rs[j].CreatedAt
			}
			return rs[i].ID < rs[j].ID
		})
		for _, r := range rs {
			detail.Replies = append(detail.Replies, ReplyView{
				ID: r.ID, PostID: r.PostID, AuthorID: r.AuthorID,
				AuthorName: r.AuthorName, Content: r.Content, CreatedAt: r.CreatedAt,
			})
		}
	})
	if !found {
		return detail, errPostNotFound
	}
	return detail, nil
}

// createReply 追加一条真实回复，并同步真实 reply_count / updated_at。
func (fs *fileStore) createReply(u *store.User, postID, raw string) (ReplyView, error) {
	content := sanitizeText(raw, maxContentRunes)
	if content == "" {
		return ReplyView{}, errContentRequired
	}
	now := nowUnix()
	var view ReplyView
	err := fs.write(func(db *database) error {
		p := findPost(db, postID)
		if p == nil {
			return errPostNotFound
		}
		r := &Reply{
			ID: store.NewID("rpl"), PostID: postID, AuthorID: u.ID,
			AuthorName: displayName(u), Content: content, CreatedAt: now,
		}
		db.Replies = append(db.Replies, r)
		p.ReplyCount++
		p.UpdatedAt = now
		view = ReplyView{
			ID: r.ID, PostID: r.PostID, AuthorID: r.AuthorID,
			AuthorName: r.AuthorName, Content: r.Content, CreatedAt: r.CreatedAt,
		}
		return nil
	})
	return view, err
}

// likePost 幂等点赞：同一 (post_id, user_id) 只计一次，重复请求返回当前真实计数。
func (fs *fileStore) likePost(u *store.User, postID string) (LikeResult, error) {
	var res LikeResult
	err := fs.write(func(db *database) error {
		p := findPost(db, postID)
		if p == nil {
			return errPostNotFound
		}
		res.PostID = postID
		res.Liked = true
		for _, l := range db.Likes {
			if l != nil && l.PostID == postID && l.UserID == u.ID {
				res.LikeCount = p.LikeCount
				return nil
			}
		}
		db.Likes = append(db.Likes, &Like{
			ID: store.NewID("lik"), PostID: postID, UserID: u.ID, CreatedAt: nowUnix(),
		})
		p.LikeCount++
		res.LikeCount = p.LikeCount
		return nil
	})
	return res, err
}

// pinPost 置顶 / 取消置顶，仅帖子作者本人（天衡所有用户统一 superuser，无管理员角色）。
func (fs *fileStore) pinPost(u *store.User, postID string, pinned bool) (PostView, error) {
	var view PostView
	err := fs.write(func(db *database) error {
		p := findPost(db, postID)
		if p == nil {
			return errPostNotFound
		}
		if u.ID != p.AuthorID {
			return errNotAuthor
		}
		p.Pinned = pinned
		p.UpdatedAt = nowUnix()
		view = PostView{
			ID: p.ID, BoardID: p.BoardID, BoardSlug: boardSlugs(db)[p.BoardID],
			AuthorID: p.AuthorID, AuthorName: p.AuthorName,
			Title: p.Title, Excerpt: excerpt(p.Content), Content: p.Content,
			Pinned: p.Pinned, LikeCount: p.LikeCount, ReplyCount: p.ReplyCount,
			CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		}
		return nil
	})
	return view, err
}

// deletePost 删除帖子（仅作者本人），级联删除其真实回复与点赞记录。
func (fs *fileStore) deletePost(u *store.User, postID string) error {
	return fs.write(func(db *database) error {
		p := findPost(db, postID)
		if p == nil {
			return errPostNotFound
		}
		if u.ID != p.AuthorID {
			return errNotAuthor
		}
		kept := db.Posts[:0]
		for _, x := range db.Posts {
			if x != nil && x.ID != postID {
				kept = append(kept, x)
			}
		}
		db.Posts = kept
		rk := db.Replies[:0]
		for _, r := range db.Replies {
			if r != nil && r.PostID != postID {
				rk = append(rk, r)
			}
		}
		db.Replies = rk
		lk := db.Likes[:0]
		for _, l := range db.Likes {
			if l != nil && l.PostID != postID {
				lk = append(lk, l)
			}
		}
		db.Likes = lk
		return nil
	})
}

// updatePost 编辑帖子（仅作者本人）：保留 created_at，刷新 updated_at，重新 sanitize。
func (fs *fileStore) updatePost(u *store.User, postID, rawTitle, rawContent string) (PostView, error) {
	title := sanitizeText(rawTitle, maxTitleRunes)
	content := sanitizeText(rawContent, maxContentRunes)
	if title == "" || content == "" {
		return PostView{}, errContentRequired
	}
	var view PostView
	err := fs.write(func(db *database) error {
		p := findPost(db, postID)
		if p == nil {
			return errPostNotFound
		}
		if u.ID != p.AuthorID {
			return errNotAuthor
		}
		p.Title = title
		p.Content = content
		p.UpdatedAt = nowUnix()
		view = PostView{
			ID: p.ID, BoardID: p.BoardID, BoardSlug: boardSlugs(db)[p.BoardID],
			AuthorID: p.AuthorID, AuthorName: p.AuthorName,
			Title: p.Title, Excerpt: excerpt(p.Content), Content: p.Content,
			Pinned: p.Pinned, LikeCount: p.LikeCount, ReplyCount: p.ReplyCount,
			CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		}
		return nil
	})
	return view, err
}

// updateReply 编辑回复（仅回复作者本人），重新 sanitize。
func (fs *fileStore) updateReply(u *store.User, replyID, raw string) (ReplyView, error) {
	content := sanitizeText(raw, maxContentRunes)
	if content == "" {
		return ReplyView{}, errContentRequired
	}
	var view ReplyView
	err := fs.write(func(db *database) error {
		for _, r := range db.Replies {
			if r != nil && r.ID == replyID {
				if u.ID != r.AuthorID {
					return errNotAuthor
				}
				r.Content = content
				view = ReplyView{
					ID: r.ID, PostID: r.PostID, AuthorID: r.AuthorID,
					AuthorName: r.AuthorName, Content: r.Content, CreatedAt: r.CreatedAt,
				}
				return nil
			}
		}
		return errReplyNotFound
	})
	return view, err
}

// deleteReply 删除回复（回复作者本人或帖子作者），真实重算 reply_count。
func (fs *fileStore) deleteReply(u *store.User, replyID string) error {
	return fs.write(func(db *database) error {
		var target *Reply
		for _, r := range db.Replies {
			if r != nil && r.ID == replyID {
				target = r
				break
			}
		}
		if target == nil {
			return errReplyNotFound
		}
		post := findPost(db, target.PostID)
		if post == nil {
			return errPostNotFound
		}
		if u.ID != target.AuthorID && u.ID != post.AuthorID {
			return errNotAuthor
		}
		k := db.Replies[:0]
		for _, r := range db.Replies {
			if r != nil && r.ID != replyID {
				k = append(k, r)
			}
		}
		db.Replies = k
		post.ReplyCount--
		post.UpdatedAt = nowUnix()
		return nil
	})
}
