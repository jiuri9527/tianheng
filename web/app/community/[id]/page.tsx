"use client";

import Link from "next/link";
import ForumPostDetail from "@/components/ForumPostDetail";

type Params = { params: { id: string } };

/** 帖子详情：数据来自 GET /api/forum/posts/{id}；未连接 / 无数据展示真实空态。 */
export default function CommunityPostPage({ params }: Params) {
  return (
    <div className="mx-auto w-full max-w-7xl px-4 py-10 md:px-6 md:py-14">
      <header className="mb-8">
        <p className="text-overline uppercase tracking-widest text-primary">Community · Post detail</p>
        <h1 className="mt-2 font-brand text-display font-bold tracking-tight text-heading">帖子详情</h1>
        <p className="mt-2 max-w-2xl text-prose text-muted-foreground">
          标题、正文、回复、点赞数与时间全部来自 /api/forum/* 的真实提交；未连接或帖子不存在时渲染真实空态。
        </p>
        <Link href="/community" className="focus-ring mt-4 inline-block rounded-sm text-sm text-muted-foreground hover:text-primary">
          ← 返回社区列表
        </Link>
      </header>
      <ForumPostDetail id={params.id} />
    </div>
  );
}
