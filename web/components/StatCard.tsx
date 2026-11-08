import type { ReactNode } from "react";

interface Props {
  label: string;
  value: ReactNode;
  sub?: ReactNode;
  icon?: ReactNode;
  tone?: "default" | "positive" | "warning" | "info" | "negative";
}

const TONE: Record<NonNullable<Props["tone"]>, string> = {
  default: "text-foreground",
  positive: "text-positive-text",
  warning: "text-warning-text",
  info: "text-info-text",
  negative: "text-negative-text",
};

export default function StatCard({ label, value, sub, icon, tone = "default" }: Props) {
  return (
    <div className="card-surface group p-5 transition-[box-shadow,transform] duration-fast ease-smooth-out hover:shadow-card-hover motion-safe:hover:-translate-y-0.5">
      <div className="flex items-start justify-between gap-3">
        <p className="text-overline uppercase tracking-widest text-muted-foreground">
          {label}
        </p>
        {icon && (
          <span className="flex size-8 items-center justify-center rounded-md bg-muted text-muted-foreground">
            {icon}
          </span>
        )}
      </div>
      <p className={`mt-4 font-brand text-display font-bold leading-none tracking-tight tabular-nums ${TONE[tone]}`}>
        {value}
      </p>
      {sub && <div className="mt-2 text-xs text-muted-foreground">{sub}</div>}
    </div>
  );
}
