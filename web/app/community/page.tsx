"use client";

import { useCallback, useEffect, useState } from "react";
import {
  forumApi,
  type BoardList,
  type MeView,
  type PostList,
  type PostView,
} from "@/components/ForumClient";
import ForumAuth from "@/components/ForumAuth";
import ForumBoardList from "@/components/ForumBoardList";
import ForumNewPostForm from "@/components/ForumNewPostForm";
import ForumPostList from "@/components/ForumPostList";

/** 社区论坛：版块 + 帖子列表/检索/分页 + 发帖 + 身份。数据全部来自 /api/forum/*。 */
export default function CommunityPage() {
  const [boards, setBoards] = useState<BoardList | null>(null);
  const [boardsLoading, setBoardsLoading] = useState(true);
  const [boardsUnavailable, setBoardsUnavailable] = useState(false);
  const [list, setList] = useState<PostList | null>(null);
  const [loading, setLoading] = useState(true);
  const [unavailable, setUnavailable] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [board, setBoard] = useState("");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(1);
  const [me, setMe] = useState<MeView | null>(null);

  const loadBoards = useCallback(async () => {
    setBoardsLoading(true);
    const res = await forumApi.boards();
    setBoardsLoading(false);
    if (res.unavailable) {
      setBoardsUnavailable(true);
      return;
    }
    setBoardsUnavailable(false);
    if (res.ok && res.data) setBoards(res.data);
  }, []);

  const loadPosts = useCallback(async () => {
    setLoading(true);
    const res = await forumApi.posts({ board, q: query, page, page_size: 20 });
    setLoading(false);
    if (res.unavailable) {
      setUnavailable(true);
      setError(null);
      return;
    }
    setUnavailable(false);
    if (!res.ok || !res.data) {
      setError(res.error?.message ?? `HTTP ${res.status}`);
      return;
    }
    setError(null);
    setList(res.data);
  }, [board, query, page]);

  useEffect(() => { void loadBoards(); }, [loadBoards]);
  useEffect(() => { void loadPosts(); }, [loadPosts]);

  const onAuthChange = useCallback((m: MeView | null) => setMe(m), []);
  const refreshAll = useCallback(() => {
    void loadBoards();
    void loadPosts();
  }, [loadBoards, loadPosts]);

  const onCreated = useCallback((_post: PostView) => {
    setPage(1);
    refreshAll();
  }, [refreshAll]);

  return (
    <div className="mx-auto w-full max-w-7xl px-4 py-10 md:px-6 md:py-14">
      <header className="mb-9">
        <p className="text-overline uppercase tracking-widest text-primary">Community · 交流论坛</p>
        <h1 className="mt-2 font-brand text-display font-bold leading-tight tracking-tight text-heading">
          社区论坛
        </h1>
        <p className="mt-2 max-w-2xl text-prose text-muted-foreground text-pretty">
          专门用于交流的社区：版块、帖子、回复、点赞、置顶、检索与分页。所有内容来自真实用户提交，
          独立持久化于 forum.json；后端未连接或暂无内容时展示真实空态，绝不填充演示数据。
        </p>
      </header>

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <main className="min-w-0 space-y-5">
          <section>
            <h2 className="mb-3 font-brand text-base font-semibold text-heading">版块</h2>
            <ForumBoardList
              boards={boards}
              loading={boardsLoading}
              unavailable={boardsUnavailable}
              activeSlug={board}
              onSelect={(slug) => { setBoard(slug); setPage(1); }}
            />
          </section>

          <section>
            <h2 className="mb-3 font-brand text-base font-semibold text-heading">帖子</h2>
            <ForumPostList
              list={list}
              loading={loading}
              unavailable={unavailable}
              error={error}
              board={board}
              query={query}
              onQueryChange={(q) => { setQuery(q); setPage(1); }}
              onPageChange={setPage}
              onRefresh={() => void loadPosts()}
              loggedIn={!!me}
            />
          </section>

          <div className="rounded-lg border border-border bg-muted/30 px-5 py-4 font-mono text-2xs leading-relaxed text-muted-foreground">
            GET /api/forum/boards · GET /api/forum/posts?board=&amp;q=&amp;page= · POST /api/forum/posts ·
            GET /api/forum/posts/&#123;id&#125; · POST /api/forum/posts/&#123;id&#125;/replies ·
            POST /api/forum/posts/&#123;id&#125;/like · PUT /api/forum/posts/&#123;id&#125;/pin ·
            DELETE /api/forum/posts/&#123;id&#125;
            <br />
            所有输出经 sanitizeText + JSON HTML 转义，页面以纯文本渲染，不回显原始 HTML。
          </div>
        </main>

        <aside className="space-y-4">
          <ForumAuth onAuthChange={onAuthChange} />
          <ForumNewPostForm boards={boards} loggedIn={!!me} onCreated={onCreated} />
        </aside>
      </div>
    </div>
  );
}
