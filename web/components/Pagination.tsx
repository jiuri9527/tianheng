import Link from "next/link";

interface Props {
  page: number;
  pages: number;
  hrefFor: (page: number) => string;
  total?: number;
}

/** SSR 分页：用真实 URL 查询参数驱动，避免把整个数据集打进客户端 bundle */
export default function Pagination({ page, pages, hrefFor, total }: Props) {
  if (pages <= 1) return null;
  const windowSize = 5;
  let start = Math.max(1, page - Math.floor(windowSize / 2));
  const end = Math.min(pages, start + windowSize - 1);
  start = Math.max(1, end - windowSize + 1);
  const nums = Array.from({ length: end - start + 1 }, (_, i) => start + i);
  const base =
    "focus-ring inline-flex min-w-9 items-center justify-center rounded-md border px-3 py-1.5 text-button transition-colors duration-quick";

  return (
    <nav
      aria-label="分页"
      className="mt-8 flex flex-wrap items-center justify-between gap-3 border-t border-border pt-5"
    >
      <p className="text-xs text-muted-foreground">
        第 {page} / {pages} 页{total !== undefined ? ` · 共 ${total} 条真实模型` : ""}
      </p>
      <div className="flex items-center gap-1">
        <Link
          href={hrefFor(Math.max(1, page - 1))}
          aria-disabled={page <= 1}
          className={`${base} border-border text-muted-foreground hover:bg-card-hover hover:text-foreground ${page <= 1 ? "pointer-events-none opacity-40" : ""}`}
        >
          上一页
        </Link>
        {nums.map((n) => (
          <Link
            key={n}
            href={hrefFor(n)}
            aria-current={n === page ? "page" : undefined}
            className={`${base} font-mono ${
              n === page
                ? "border-primary bg-primary text-primary-foreground"
                : "border-border text-muted-foreground hover:bg-card-hover hover:text-foreground"
            }`}
          >
            {n}
          </Link>
        ))}
        <Link
          href={hrefFor(Math.min(pages, page + 1))}
          aria-disabled={page >= pages}
          className={`${base} border-border text-muted-foreground hover:bg-card-hover hover:text-foreground ${page >= pages ? "pointer-events-none opacity-40" : ""}`}
        >
          下一页
        </Link>
      </div>
    </nav>
  );
}
