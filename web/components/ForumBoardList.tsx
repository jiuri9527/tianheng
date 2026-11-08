"use client";

import type { BoardList } from "./ForumClient";
import ForumEmptyState from "./ForumEmptyState";

interface Props {
  boards: BoardList | null;
  loading: boolean;
  unavailable: boolean;
  activeSlug: string;
  onSelect: (slug: string) => void;
}

/** 版块列表。版块由真实发帖提交产生，初始为空数组 -> 真实空态。 */
export default function ForumBoardList({ boards, loading, unavailable, activeSlug, onSelect }: Props) {
  if (unavailable) {
    return <ForumEmptyState kind="unavailable" />;
  }
  if (loading && !boards) {
    return <div className="card-surface px-5 py-8 text-center text-sm text-muted-foreground">正在读取 /api/forum/boards …</div>;
  }
  const items = boards?.items ?? [];
  if (items.length === 0) {
    return (
      <ForumEmptyState
        kind="empty"
        title="还没有版块"
        description="版块不预置任何内置分类；当你提交第一帖并填写 board slug 时，版块由这次真实提交创建。"
      />
    );
  }
  return (
    <div className="flex flex-wrap gap-2">
      <button
        type="button"
        onClick={() => onSelect("")}
        className={`focus-ring rounded-sm border px-3 py-1.5 text-xs transition-colors duration-quick ${activeSlug === "" ? "border-primary bg-primary text-primary-foreground" : "border-border bg-card text-muted-foreground hover:bg-card-hover hover:text-foreground"}`}
      >
        全部
      </button>
      {items.map((b) => (
        <button
          key={b.id}
          type="button"
          onClick={() => onSelect(b.slug)}
          title={b.description}
          className={`focus-ring rounded-sm border px-3 py-1.5 text-xs transition-colors duration-quick ${activeSlug === b.slug ? "border-primary bg-primary text-primary-foreground" : "border-border bg-card text-muted-foreground hover:bg-card-hover hover:text-foreground"}`}
        >
          {/* 版块名 / 描述均以纯文本渲染 */}
          {b.name}
          <span className="ml-1.5 font-mono text-2xs opacity-70">{b.post_count}</span>
        </button>
      ))}
    </div>
  );
}
