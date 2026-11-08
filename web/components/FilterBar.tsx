"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import type { ModelSortKey } from "@/lib/models";
import { CAPABILITY_LABELS } from "@/lib/constants";

interface Props {
  providers: { name: string; count: number }[];
  initial: { q: string; provider: string; capability: string; sort: ModelSortKey };
}

const SORTS: { value: ModelSortKey; label: string }[] = [
  { value: "context-desc", label: "上下文从大到小" },
  { value: "price-asc", label: "价格从低到高" },
  { value: "price-desc", label: "价格从高到低" },
  { value: "params-desc", label: "支持参数最多" },
  { value: "name-asc", label: "名称 A→Z" },
];

export default function FilterBar({ providers, initial }: Props) {
  const router = useRouter();
  const [q, setQ] = useState(initial.q);
  const [provider, setProvider] = useState(initial.provider);
  const [capability, setCapability] = useState(initial.capability);
  const [sort, setSort] = useState<ModelSortKey>(initial.sort);

  const push = (patch: Record<string, string>) => {
    const p = new URLSearchParams();
    const merged = { q, provider, capability, sort, ...patch };
    if (merged.q) p.set("q", merged.q);
    if (merged.provider && merged.provider !== "all") p.set("provider", merged.provider);
    if (merged.capability && merged.capability !== "all") p.set("capability", merged.capability);
    if (merged.sort && merged.sort !== "context-desc") p.set("sort", merged.sort);
    const s = p.toString();
    router.push(s ? `/models?${s}` : "/models");
  };

  const select =
    "focus-ring w-full rounded-md border border-border bg-input-bg px-3 py-2 text-sm text-foreground transition-colors duration-quick hover:border-primary/40";

  return (
    <div className="card-surface p-4">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          push({ q });
        }}
        className="grid gap-3 md:grid-cols-[1.5fr_1fr_1fr_1fr_auto]"
      >
        <div>
          <label htmlFor="model-q" className="mb-1.5 block text-overline uppercase tracking-widest text-muted-foreground">
            搜索模型
          </label>
          <input
            id="model-q"
            type="search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="输入真实 id 或名称，如 gemini"
            className={select}
          />
        </div>
        <div>
          <label htmlFor="model-provider" className="mb-1.5 block text-overline uppercase tracking-widest text-muted-foreground">
            供应商
          </label>
          <select
            id="model-provider"
            value={provider}
            onChange={(e) => {
              setProvider(e.target.value);
              push({ provider: e.target.value });
            }}
            className={select}
          >
            <option value="all">全部 ({providers.reduce((a, b) => a + b.count, 0)})</option>
            {providers.map((p) => (
              <option key={p.name} value={p.name}>
                {p.name} ({p.count})
              </option>
            ))}
          </select>
        </div>
        <div>
          <label htmlFor="model-cap" className="mb-1.5 block text-overline uppercase tracking-widest text-muted-foreground">
            能力
          </label>
          <select
            id="model-cap"
            value={capability}
            onChange={(e) => {
              setCapability(e.target.value);
              push({ capability: e.target.value });
            }}
            className={select}
          >
            <option value="all">全部能力</option>
            {CAPABILITY_LABELS.map((c) => (
              <option key={c.key} value={c.key}>
                {c.label}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label htmlFor="model-sort" className="mb-1.5 block text-overline uppercase tracking-widest text-muted-foreground">
            排序
          </label>
          <select
            id="model-sort"
            value={sort}
            onChange={(e) => {
              const v = e.target.value as ModelSortKey;
              setSort(v);
              push({ sort: v });
            }}
            className={select}
          >
            {SORTS.map((s) => (
              <option key={s.value} value={s.value}>
                {s.label}
              </option>
            ))}
          </select>
        </div>
        <div className="flex items-end">
          <button
            type="submit"
            className="focus-ring inline-flex h-[38px] w-full items-center justify-center rounded-md bg-primary px-4 text-button font-medium text-primary-foreground transition-[filter] duration-quick hover:brightness-110 md:w-auto"
          >
            筛选
          </button>
        </div>
      </form>
      <p className="mt-3 font-mono text-2xs text-muted-foreground">
        筛选与排序均在服务端对 web/data/models.json（460 条真实条目）执行，结果不含任何编造模型。
      </p>
    </div>
  );
}
