"use client";

import { useState, type FormEvent } from "react";
import {
  forumApi,
  FORUM_UNAVAILABLE_HINT,
  type BoardList,
  type PostView,
} from "./ForumClient";

interface Props {
  boards: BoardList | null;
  loggedIn: boolean;
  onCreated: (post: PostView) => void;
}

/** 发帖表单。board slug 由用户真实提交；首次提交会由后端创建该版块（非种子）。 */
export default function ForumNewPostForm({ boards, loggedIn, onCreated }: Props) {
  const [board, setBoard] = useState("");
  const [boardName, setBoardName] = useState("");
  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const inputCls =
    "focus-ring w-full rounded-md border border-border bg-input-bg px-3 py-2 text-sm text-foreground transition-colors duration-quick hover:border-primary/40";

  const submit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!loggedIn) {
      setMsg({ ok: false, text: "请先登录后再发帖。" });
      return;
    }
    setBusy(true);
    setMsg(null);
    const res = await forumApi.createPost({
      board: board.trim(),
      board_name: boardName.trim() || undefined,
      title,
      content,
    });
    setBusy(false);
    if (res.unavailable) {
      setMsg({ ok: false, text: FORUM_UNAVAILABLE_HINT });
      return;
    }
    if (!res.ok || !res.data) {
      setMsg({ ok: false, text: res.error?.message ?? `HTTP ${res.status}` });
      return;
    }
    setTitle("");
    setContent("");
    setBoardName("");
    setMsg({ ok: true, text: `已发布：${res.data.id}（版块 / 时间 / 作者均来自本次真实提交）` });
    onCreated(res.data);
  };

  const existing = boards?.items ?? [];

  return (
    <form onSubmit={(e) => void submit(e)} className="card-surface space-y-3 p-5">
      <div>
        <p className="text-overline uppercase tracking-widest text-primary">New post</p>
        <h2 className="mt-1 font-brand text-base font-semibold text-heading">发布新帖</h2>
        <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
          所有字段来自你的真实输入；服务端会把 HTML 标签净化为纯文本，不会回显原始 HTML。
        </p>
      </div>

      <div>
        <label htmlFor="forum-board" className="mb-1 block text-2xs text-muted-foreground">版块 slug（小写字母 / 数字 / 连字符）</label>
        <input
          id="forum-board"
          required
          value={board}
          onChange={(e) => setBoard(e.target.value)}
          placeholder="例如：general"
          pattern="[a-z0-9][a-z0-9-]{0,31}"
          className={inputCls}
          list={existing.length > 0 ? "forum-board-options" : undefined}
        />
        {existing.length > 0 && (
          <datalist id="forum-board-options">
            {existing.map((b) => <option key={b.id} value={b.slug}>{b.name}</option>)}
          </datalist>
        )}
        <p className="mt-1 text-2xs text-muted-foreground">
          {existing.length === 0
            ? "当前没有任何版块；填写 slug 后，本条真实提交会创建第一个版块。"
            : "可选用已有版块，或填写新 slug 创建新板块。"}
        </p>
      </div>

      <div>
        <label htmlFor="forum-board-name" className="mb-1 block text-2xs text-muted-foreground">版块显示名（可选，仅首次创建时生效）</label>
        <input id="forum-board-name" value={boardName} onChange={(e) => setBoardName(e.target.value)} placeholder="例如：综合讨论" className={inputCls} />
      </div>

      <div>
        <label htmlFor="forum-title" className="mb-1 block text-2xs text-muted-foreground">标题</label>
        <input id="forum-title" required maxLength={200} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="想聊点什么" className={inputCls} />
      </div>

      <div>
        <label htmlFor="forum-content" className="mb-1 block text-2xs text-muted-foreground">正文（纯文本）</label>
        <textarea id="forum-content" required rows={5} maxLength={20000} value={content} onChange={(e) => setContent(e.target.value)} placeholder="写下你的真实内容…" className={`${inputCls} resize-y`} />
      </div>

      <button type="submit" disabled={busy || !loggedIn} className="focus-ring w-full rounded-md bg-primary px-4 py-2.5 text-button font-medium text-primary-foreground transition-[filter] duration-quick hover:brightness-110 disabled:opacity-60">
        {!loggedIn ? "登录后发帖" : busy ? "提交中…" : "发布"}
      </button>

      {msg && (
        <div role="status" className={`rounded-md border px-3 py-2 text-xs leading-relaxed ${msg.ok ? "border-positive/30 bg-positive/8 text-positive-text" : "border-warning/30 bg-warning/8 text-warning-text"}`}>
          {msg.text}
        </div>
      )}
    </form>
  );
}
