"use client";

import Link from "next/link";
import { useState, type FormEvent } from "react";
import {
  forumApi,
  formatUnix,
  FORUM_UNAVAILABLE_HINT,
  type PostList,
} from "./ForumClient";
import ForumEmptyState from "./ForumEmptyState";

interface Props {
  list: PostList | null;
  loading: boolean;
  unavailable: boolean;
  error: string | null;
  board: string;
  query: string;
  onQueryChange: (q: string) => void;
  onPageChange: (page: number) => void;
  onRefresh: () => void;
  loggedIn: boolean;
}

export default function ForumPostList({
  list, loading, unavailable, error, board, query,
  onQueryChange, onPageChange, onRefresh, loggedIn,
}: Props) {
  const [draft, setDraft] = useState(query);
  const [busy, setBusy] = useState("");
  const [notice, setNotice] = useState<string | null>(null);

  const submitSearch = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    onQueryChange(draft.trim());
  };

  const doLike = async (id: string) => {
    if (!loggedIn) {
      setNotice("点赞需要登录（右侧社区身份面板）。");
      return;
    }
    setBusy(id);
    const res = await forumApi.like(id);
    setBusy("");
    if (res.unavailable) {
      setNotice(FORUM_UNAVAILABLE_HINT);
      return;
    }
    if (!res.ok) {
      setNotice(res.error?.message ?? `HTTP ${res.status}`);
      return;
    }
    setNotice(null);
    onRefresh();
  };

  if (unavailable) return <ForumEmptyState kind="unavailable" />;
  if (error) return <ForumEmptyState kind="error" description={error} />;
  if (loading && !list) {
    return <div className="card-surface px-5 py-10 text-center text-sm text-muted-foreground">正在读取 /api/forum/posts …</div>;
  }

  const items = list?.items ?? [];
  const total = list?.total ?? 0;
  const page = list?.page ?? 1;
  const pageSize = list?.page_size ?? 20;
  const pages = Math.max(1, Math.ceil(total / pageSize));

  return (
    <div>
      <form onSubmit={submitSearch} className="mb-4 flex flex-wrap gap-2">
        <input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder="检索标题 / 正文关键词"
          aria-label="检索帖子"
          className="focus-ring min-w-0 flex-1 rounded-md border border-border bg-input-bg px-3 py-2 text-sm text-foreground transition-colors duration-quick hover:border-primary/40"
        />
        <button type="submit" className="focus-ring rounded-md bg-primary px-4 py-2 text-button font-medium text-primary-foreground transition-[filter] duration-quick hover:brightness-110">
          检索
        </button>
        {query && (
          <button type="button" onClick={() => { setDraft(""); onQueryChange(""); }} className="focus-ring rounded-md border border-border bg-card px-3 py-2 text-button text-muted-foreground transition-colors duration-quick hover:bg-card-hover">
            清除
          </button>
        )}
      </form>

      <p className="mb-3 font-mono text-2xs text-muted-foreground">
        GET /api/forum/posts · board={board || "全部"} · q={query || "—"} · total={total} · page={page}/{pages}
      </p>

      {notice && (
        <div role="status" className="mb-3 rounded-md border border-warning/30 bg-warning/8 px-3 py-2 text-xs text-warning-text">{notice}</div>
      )}

      {items.length === 0 ? (
        <ForumEmptyState
          kind="empty"
          title="没有匹配的帖子"
          description="论坛没有任何演示数据。帖子、回复、点赞与时间都来自真实用户提交；可在右侧登录后发布第一帖。"
        />
      ) : (
        <ul className="space-y-3">
          {items.map((p) => (
            <li key={p.id} className="card-surface p-5">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="mb-1.5 flex flex-wrap items-center gap-2">
                    {p.pinned && <span className="rounded-sm border border-primary/40 bg-accent px-2 py-0.5 text-2xs text-accent-foreground">置顶</span>}
                    <span className="rounded-sm border border-border bg-muted/60 px-2 py-0.5 font-mono text-2xs text-muted-foreground">{p.board_slug}</span>
                  </div>
                  {/* 标题 / 作者 / 摘要均为纯文本渲染（React 自动转义） */}
                  <Link href={`/community/${encodeURIComponent(p.id)}`} className="focus-ring rounded-sm font-brand text-base font-semibold text-heading hover:text-primary">
                    {p.title}
                  </Link>
                  <p className="mt-1.5 line-clamp-2 text-sm leading-relaxed text-muted-foreground">{p.excerpt}</p>
                  <p className="mt-2 font-mono text-2xs text-muted-foreground">
                    {p.author_name} · {formatUnix(p.created_at)} · {p.reply_count} 回复 · {p.like_count} 赞
                  </p>
                </div>
                <button
                  type="button"
                  disabled={busy === p.id}
                  onClick={() => void doLike(p.id)}
                  className={`focus-ring shrink-0 rounded-sm border px-3 py-1.5 text-xs transition-colors duration-quick disabled:opacity-60 ${p.liked ? "border-primary/40 bg-accent text-accent-foreground" : "border-border bg-card text-muted-foreground hover:bg-card-hover hover:text-foreground"}`}
                >
                  {p.liked ? "已赞" : "赞"} {p.like_count}
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}

      {pages > 1 && (
        <nav aria-label="帖子分页" className="mt-6 flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4">
          <button type="button" disabled={page <= 1} onClick={() => onPageChange(page - 1)} className="focus-ring rounded-md border border-border bg-card px-3 py-1.5 text-button disabled:opacity-40">
            上一页
          </button>
          <span className="font-mono text-2xs text-muted-foreground">第 {page} / {pages} 页 · 共 {total} 条真实帖子</span>
          <button type="button" disabled={page >= pages} onClick={() => onPageChange(page + 1)} className="focus-ring rounded-md border border-border bg-card px-3 py-1.5 text-button disabled:opacity-40">
            下一页
          </button>
        </nav>
      )}
    </div>
  );
}
