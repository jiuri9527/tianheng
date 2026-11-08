import type { Metadata } from "next";
import ConsoleLayout from "@/components/ConsoleLayout";
import LogsTable from "@/components/LogsTable";

export const metadata: Metadata = { title: "请求日志" };

const SCHEMA = [
  "request_id", "user_id", "token_id", "alias", "model_id", "channel_id",
  "provider_type", "stream", "request_body", "response_body", "prompt_tokens",
  "completion_tokens", "ttft_ms", "latency_ms", "status_code", "error_code",
  "cost_usd", "client_ip", "created_at",
];

export default function LogsPage() {
  return (
    <ConsoleLayout
      eyebrow="Request Logs · F10 / F11"
      title="请求日志"
      description="每次调用记录完整输入 messages、完整输出 content、prompt/completion tokens、TTFT、总耗时、状态码、命中渠道、花费与 request_id。按 request_id / 用户 / 模型 / 状态码 / 关键字检索。"
    >
      <LogsTable />

      <section className="mt-10">
        <h2 className="mb-4 font-brand text-base font-semibold text-heading">request_logs 字段契约</h2>
        <div className="card-surface flex flex-wrap gap-1.5 p-5">
          {SCHEMA.map((f) => (
            <span key={f} className="rounded-sm border border-border bg-muted/60 px-2 py-1 font-mono text-2xs text-muted-foreground">{f}</span>
          ))}
        </div>
        <p className="mt-3 font-mono text-2xs leading-relaxed text-muted-foreground">
          索引：(user_id, created_at DESC) / (alias, created_at DESC) / (request_id) · X-Request-Id 全链路 ·
          LOG_FULL_BODY / LOG_MASK_KEYS 可在 /admin/settings 配置。
        </p>
      </section>
    </ConsoleLayout>
  );
}
