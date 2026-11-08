"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import {
  forumApi,
  formatUnix,
  getForumToken,
  FORUM_UNAVAILABLE_HINT,
  type MeView,
  type PostDetail,
} from "./ForumClient";
import ForumEmptyState from "./ForumEmptyState";
import ForumReplyForm from "./ForumReplyForm";

interface Props {
  id: string;
}

/** 帖子详情 + 真实回复列表 + 点赞 / 置顶 / 删除。 */
export default function ForumPostDetail({ id }: Props) {
  const router = useRouter();
  const [detail, setDetail] = useState<PostDetail | null>(null);
  const [me, setMe] = useState<MeView | null>(null);
  const [loading, setLoading] = useState(true);
  const [unavailable, setUnavailable] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    const [d, m] = await Promise.all([forumApi.getPost(id), forumApi.me()]);
    setLoading(false);
    setMe(m.ok && m.data ? m.data : null);
    if (d.unavailable) {
      setUnavailable(true);
      setDetail(null);
      return;
    }
    setUnavailable(false);
    if (!d.ok || !d.data) {
      setError(d.error?.message ?? `HTTP ${d.status}`);
      setDetail(null);
      return;
    }
    setError(null);
    setDetail(d.data);
  }, [id]);

  useEffect(() => {
    void load();
  }, [load]);

  const act = async (kind: "like" | "pin" | "remove") => {
    if (!detail) return;
    if (!getForumToken()) {
      setNotice("需要登录（可在社区页的身份面板登录）。");
      return;
    }
    setBusy(kind);
    setNotice(null);
    if (kind === "like") {
      const res = await forumApi.like(id);
      setBusy("");
      if (res.unavailable) { setNotice(FORUM_UNAVAILABLE_HINT); return; }
      if (!res.ok) { setNotice(res.error?.message ?? `HTTP ${res.status}`); return; }
      await load();
      return;
    }
    if (kind === "pin") {
      const res = await forumApi.pin(id, !detail.post.pinned);
      setBusy("");
      if (res.unavailable) { setNotice(FORUM_UNAVAILABLE_HINT); return; }
      if (!res.ok) { setNotice(res.error?.message ?? `HTTP ${res.status}`); return; }
      await load();
      return;
    }
    const res = await forumApi.remove(id);
    setBusy("");
    if (res.unavailable) { setNotice(FORUM_UNAVAILABLE_HINT); return; }
    if (!res.ok) { setNotice(res.error?.message ?? `HTTP ${res.status}`); return; }
    router.push("/community");
  };

  if (unavailable) return <ForumEmptyState kind="unavailable" />;
  if (error) return <ForumEmptyState kind="error" description={error} action={<Link href="/community" className="focus-ring rounded-md bg-primary px-4 py-2 text-button text-primary-foreground">返回社区</Link>} />;
  if (loading && !detail) {
    return <div className="card-surface px-5 py-10 text-center text-sm text-muted-foreground">正在读取 /api/forum/posts/{id} …</div>;
  }
  if (!detail) return <ForumEmptyState kind="empty" title="帖子不存在" description="没有找到该帖子；页面不展示任何编造内容。" />;

  const p = detail.post;
  // 天衡所有用户统一 superuser（无管理员角色）：置顶 / 删除仅帖子作者本人可用。
  const isAuthor = !!me && me.id === p.author_id;
  const canPin = isAuthor;
  const canDelete = isAuthor;
  const loggedIn = !!me;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Link href="/community" className="focus-ring rounded-sm text-sm text-muted-foreground hover:text-primary">← 返回社区</Link>
        <span className="font-mono text-2xs text-muted-foreground">GET /api/forum/posts/{id}</span>
      </div>

      {notice && (
        <div role="status" className="rounded-md border border-warning/30 bg-warning/8 px-3 py-2 text-xs text-warning-text">{notice}</div>
      )}

      <article className="card-surface p-6">
        <div className="mb-3 flex flex-wrap items-center gap-2">
          {p.pinned && <span className="rounded-sm border border-primary/40 bg-accent px-2 py-0.5 text-2xs text-accent-foreground">置顶</span>}
          <span className="rounded-sm border border-border bg-muted/60 px-2 py-0.5 font-mono text-2xs text-muted-foreground">{p.board_slug}</span>
          <span className="font-mono text-2xs text-muted-foreground">{p.id}</span>
        </div>
        {/* 所有用户内容均为纯文本渲染，绝不 dangerouslySetInnerHTML */}
        <h1 className="font-brand text-2xl font-bold tracking-tight text-heading">{p.title}</h1>
        <p className="mt-2 font-mono text-2xs text-muted-foreground">
          {p.author_name} · {formatUnix(p.created_at)} · {p.reply_count} 回复 · {p.like_count} 赞
          {p.updated_at !== p.created_at ? ` · 更新 ${formatUnix(p.updated_at)}` : ""}
        </p>
        <div className="mt-5 whitespace-pre-wrap break-words text-sm leading-relaxed text-foreground">{p.content}</div>

        <div className="mt-6 flex flex-wrap gap-2 border-t border-border pt-4">
          <button type="button" disabled={busy === "like"} onClick={() => void act("like")} className={`focus-ring rounded-md border px-3 py-1.5 text-xs transition-colors duration-quick disabled:opacity-60 ${p.liked ? "border-primary/40 bg-accent text-accent-foreground" : "border-border bg-card text-muted-foreground hover:bg-card-hover"}`}>
            {p.liked ? "已赞" : "赞"} {p.like_count}
          </button>
          {canPin && (
            <button type="button" disabled={busy === "pin"} onClick={() => void act("pin")} className="focus-ring rounded-md border border-border bg-card px-3 py-1.5 text-xs text-muted-foreground transition-colors duration-quick hover:bg-card-hover disabled:opacity-60">
              {p.pinned ? "取消置顶" : "置顶"}
            </button>
          )}
          {canDelete && (
            <button type="button" disabled={busy === "remove"} onClick={() => void act("remove")} className="focus-ring rounded-md border border-negative/30 bg-negative/8 px-3 py-1.5 text-xs text-negative-text transition-colors duration-quick hover:brightness-105 disabled:opacity-60">
              删除
            </button>
          )}
        </div>
      </article>

      <section>
        <h2 className="mb-3 font-brand text-base font-semibold text-heading">回复（{detail.replies.length}）</h2>
        {detail.replies.length === 0 ? (
          <ForumEmptyState kind="empty" title="还没有回复" description="回复全部来自真实用户提交；登录后可发表第一条回复。" />
        ) : (
          <ul className="space-y-3">
            {detail.replies.map((r) => (
              <li key={r.id} className="card-surface p-4">
                <p className="font-mono text-2xs text-muted-foreground">{r.author_name} · {formatUnix(r.created_at)}</p>
                <p className="mt-2 whitespace-pre-wrap break-words text-sm leading-relaxed text-foreground">{r.content}</p>
              </li>
            ))}
          </ul>
        )}
      </section>

      <ForumReplyForm postId={id} loggedIn={loggedIn} onReplied={() => void load()} />
    </div>
  );
}
