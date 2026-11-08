"use client";

import type { ReactNode } from "react";
import { FORUM_UNAVAILABLE_HINT } from "./ForumClient";

interface Props {
  kind?: "empty" | "unavailable" | "error";
  title?: string;
  description?: string;
  action?: ReactNode;
}

const PRESET: Record<NonNullable<Props["kind"]>, { title: string; description: string }> = {
  empty: {
    title: "这里还没有内容",
    description:
      "论坛不预置任何演示帖子 / 版块 / 点赞数。第一条内容需要由真实用户提交后才会出现。",
  },
  unavailable: {
    title: "后端未连接",
    description: FORUM_UNAVAILABLE_HINT,
  },
  error: {
    title: "请求未完成",
    description: "服务端返回了错误，未填充任何占位数据。",
  },
};

/** 真实空态：无数据 / 未连接 / 出错时渲染，绝不填充编造内容。 */
export default function ForumEmptyState({ kind = "empty", title, description, action }: Props) {
  const p = PRESET[kind];
  return (
    <div className="card-surface flex flex-col items-center justify-center gap-3 px-6 py-12 text-center">
      <span className="flex size-11 items-center justify-center rounded-lg border border-border bg-muted text-muted-foreground" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" className="size-5">
          <path d="M4 5.5h16v11H9l-5 3v-14Z" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
          <path d="M8 9.5h8M8 12.5h5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
        </svg>
      </span>
      <h3 className="font-brand text-base font-semibold text-heading">{title ?? p.title}</h3>
      <p className="max-w-md text-sm leading-relaxed text-muted-foreground">{description ?? p.description}</p>
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}
