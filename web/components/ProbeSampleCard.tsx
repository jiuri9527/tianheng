import type { ModelProbe } from "@/lib/types";
import { ProbeStatusBadge } from "@/components/StatusBadge";

/** F4 规范定义的探测请求体（真实规范常量，不是编造的模型输出） */
export const F4_PROBE_REQUEST_TEMPLATE = `{
  "model": "<upstream_id>",
  "messages": [
    { "role": "user", "content": "Reply with the single word: OK" }
  ],
  "max_tokens": 16,
  "stream": false
}`;

/**
 * 真实输入样例 / 真实输出样例 / 实测延迟。
 * 无真实探测数据时显式标记 probe_status=unavailable，绝不填充编造样本。
 */
export default function ProbeSampleCard({
  modelId,
  probe,
}: {
  modelId: string;
  probe: ModelProbe | null;
}) {
  const hasRealProbe = Boolean(probe && probe.ok && probe.output !== null);

  return (
    <div className="card-surface overflow-hidden">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-muted/40 px-4 py-3">
        <h4 className="text-sm font-semibold text-heading">真实探测数据 (F4)</h4>
        <ProbeStatusBadge status={hasRealProbe ? "ok" : probe?.ok === false ? "failed" : "unavailable"} />
      </div>

      <div className="grid gap-4 p-4 md:grid-cols-2">
        <div>
          <p className="text-overline uppercase tracking-widest text-muted-foreground">
            真实输入样例
          </p>
          <pre className="mt-2 overflow-x-auto rounded-md border border-border bg-muted/40 p-3 font-mono text-2xs leading-relaxed text-foreground/90">
{probe?.input ? JSON.stringify(probe.input, null, 2) : F4_PROBE_REQUEST_TEMPLATE.replace("<upstream_id>", modelId)}
          </pre>
          {!probe?.input && (
            <p className="mt-1.5 text-2xs text-muted-foreground">
              上方为 F4 规范定义的探测请求模板；实际请求体由 POST /api/models/:id/probe 采集。
            </p>
          )}
        </div>
        <div>
          <p className="text-overline uppercase tracking-widest text-muted-foreground">
            真实输出样例
          </p>
          {hasRealProbe ? (
            <pre className="mt-2 overflow-x-auto rounded-md border border-border bg-muted/40 p-3 font-mono text-2xs leading-relaxed text-foreground/90">
{probe?.output}
            </pre>
          ) : (
            <div className="mt-2 flex min-h-[7rem] flex-col items-center justify-center gap-2 rounded-md border border-dashed border-border bg-muted/20 px-4 text-center">
              <span className="font-mono text-2xs text-muted-foreground">probe_status=unavailable</span>
              <p className="max-w-xs text-2xs leading-relaxed text-muted-foreground">
                尚未对该模型执行真实探测，输出样例为空。点击「重新探测」触发一次真实上游调用后写入。
              </p>
            </div>
          )}
        </div>
      </div>

      <div className="grid grid-cols-2 gap-4 border-t border-border px-4 py-3 sm:grid-cols-4">
        <Metric label="实测延迟" value={probe?.latency_ms != null ? `${probe.latency_ms} ms` : "未采集"} />
        <Metric label="prompt_tokens" value={probe?.prompt_tokens != null ? String(probe.prompt_tokens) : "未采集"} />
        <Metric label="completion_tokens" value={probe?.completion_tokens != null ? String(probe.completion_tokens) : "未采集"} />
        <Metric label="probed_at" value={probe?.probed_at ?? "未采集"} />
      </div>
      {probe?.error && (
        <p className="border-t border-border bg-negative/8 px-4 py-2 font-mono text-2xs text-negative-text">
          error: {probe.error}
        </p>
      )}
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-overline uppercase tracking-widest text-muted-foreground">{label}</p>
      <p className="mt-1 truncate font-mono text-xs tabular-nums text-foreground" title={value}>
        {value}
      </p>
    </div>
  );
}
