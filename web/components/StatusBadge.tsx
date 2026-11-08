import type { ChannelStatus, ProbeStatus, Visibility } from "@/lib/types";

type Tone = "positive" | "warning" | "negative" | "info" | "neutral";

const TONE_CLASS: Record<Tone, string> = {
  positive: "bg-positive/12 text-positive-text border-positive/20",
  warning: "bg-warning/12 text-warning-text border-warning/14",
  negative: "bg-negative/12 text-negative-text border-negative/20",
  info: "bg-info/12 text-info-text border-info/14",
  neutral: "bg-muted text-muted-foreground border-border",
};

export function Badge({
  children,
  tone = "neutral",
  mono = false,
}: {
  children: React.ReactNode;
  tone?: Tone;
  mono?: boolean;
}) {
  return (
    <span
      className={`inline-flex items-center rounded-full border px-2 py-0.5 text-overline font-medium ${TONE_CLASS[tone]} ${mono ? "font-mono" : ""}`}
    >
      {children}
    </span>
  );
}

export function ProviderBadge({ provider }: { provider: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 rounded-sm border border-border bg-muted/60 px-2 py-1 font-mono text-2xs text-muted-foreground">
      <span className="size-1.5 rounded-full bg-primary" aria-hidden="true" />
      {provider}
    </span>
  );
}

const CHANNEL_TONE: Record<ChannelStatus, Tone> = {
  active: "positive",
  disabled: "neutral",
  unhealthy: "negative",
  pending: "warning",
};

const CHANNEL_LABEL: Record<ChannelStatus, string> = {
  active: "健康",
  disabled: "已停用",
  unhealthy: "异常",
  pending: "待检查",
};

export function ChannelStatusBadge({ status }: { status: ChannelStatus }) {
  return <Badge tone={CHANNEL_TONE[status]}>{CHANNEL_LABEL[status]}</Badge>;
}

const PROBE_TONE: Record<ProbeStatus, Tone> = {
  ok: "positive",
  failed: "negative",
  pending: "warning",
  unavailable: "neutral",
};

const PROBE_LABEL: Record<ProbeStatus, string> = {
  ok: "已采集",
  failed: "探测失败",
  pending: "探测中",
  unavailable: "未采集",
};

export function ProbeStatusBadge({ status }: { status: ProbeStatus }) {
  return <Badge tone={PROBE_TONE[status]}>{PROBE_LABEL[status]}</Badge>;
}

export function VisibilityBadge({ visibility }: { visibility: Visibility }) {
  return (
    <Badge tone={visibility === "public" ? "info" : "neutral"}>
      {visibility === "public" ? "公开渠道" : "私有渠道"}
    </Badge>
  );
}
