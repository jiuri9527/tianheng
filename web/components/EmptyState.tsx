interface Props {
  title: string;
  description?: string;
  hint?: string;
  icon?: React.ReactNode;
  action?: React.ReactNode;
}

/** 真实空态：后端 /api/* 未连接时渲染，明确说明数据来源，不填充编造行 */
export default function EmptyState({ title, description, hint, icon, action }: Props) {
  return (
    <div className="card-surface flex flex-col items-center justify-center gap-3 px-6 py-14 text-center">
      <span className="flex size-11 items-center justify-center rounded-lg border border-border bg-muted text-muted-foreground">
        {icon ?? (
          <svg viewBox="0 0 24 24" fill="none" aria-hidden="true" className="size-5">
            <rect x="3.5" y="4.5" width="17" height="15" rx="2" stroke="currentColor" strokeWidth="1.4" />
            <path d="M3.5 9.5h17M8 4.5v15" stroke="currentColor" strokeWidth="1.4" />
          </svg>
        )}
      </span>
      <h3 className="font-brand text-base font-semibold text-heading">{title}</h3>
      {description && (
        <p className="max-w-md text-sm leading-relaxed text-muted-foreground">
          {description}
        </p>
      )}
      {hint && (
        <code className="mt-1 rounded-sm border border-border bg-muted/60 px-2 py-1 font-mono text-2xs text-muted-foreground">
          {hint}
        </code>
      )}
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}
