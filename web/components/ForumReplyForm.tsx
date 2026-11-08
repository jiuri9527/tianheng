"use client";

import { useState, type FormEvent } from "react";
import { forumApi, FORUM_UNAVAILABLE_HINT } from "./ForumClient";

interface Props {
  postId: string;
  loggedIn: boolean;
  onReplied: () => void;
}

/** 回复表单：内容来自真实输入，服务端净化后落库。 */
export default function ForumReplyForm({ postId, loggedIn, onReplied }: Props) {
  const [content, setContent] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const submit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!loggedIn) {
      setMsg({ ok: false, text: "请先登录后再回复。" });
      return;
    }
    setBusy(true);
    setMsg(null);
    const res = await forumApi.reply(postId, content);
    setBusy(false);
    if (res.unavailable) {
      setMsg({ ok: false, text: FORUM_UNAVAILABLE_HINT });
      return;
    }
    if (!res.ok) {
      setMsg({ ok: false, text: res.error?.message ?? `HTTP ${res.status}` });
      return;
    }
    setContent("");
    setMsg({ ok: true, text: "回复已发布（真实提交）。" });
    onReplied();
  };

  return (
    <form onSubmit={(e) => void submit(e)} className="card-surface space-y-3 p-5">
      <h2 className="font-brand text-base font-semibold text-heading">发表回复</h2>
      <textarea
        required
        rows={4}
        maxLength={20000}
        value={content}
        onChange={(e) => setContent(e.target.value)}
        placeholder="写下你的真实回复…"
        aria-label="回复内容"
        className="focus-ring w-full resize-y rounded-md border border-border bg-input-bg px-3 py-2 text-sm text-foreground transition-colors duration-quick hover:border-primary/40"
      />
      <button type="submit" disabled={busy || !loggedIn} className="focus-ring rounded-md bg-primary px-4 py-2 text-button font-medium text-primary-foreground transition-[filter] duration-quick hover:brightness-110 disabled:opacity-60">
        {!loggedIn ? "登录后回复" : busy ? "提交中…" : "回复"}
      </button>
      {msg && (
        <div role="status" className={`rounded-md border px-3 py-2 text-xs ${msg.ok ? "border-positive/30 bg-positive/8 text-positive-text" : "border-warning/30 bg-warning/8 text-warning-text"}`}>
          {msg.text}
        </div>
      )}
    </form>
  );
}
