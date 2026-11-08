import Link from "next/link";
import type { Metadata } from "next";
import {
  capabilitiesOf,
  CAPABILITY_LABELS,
  completionPricePerMtok,
  decodeModelId,
  formatContext,
  formatUsd,
  getModelById,
  promptPricePerMtok,
  providerOf,
  ALL_MODELS,
} from "@/lib/models";
import ConsoleLayout from "@/components/ConsoleLayout";
import CodeBlock from "@/components/CodeBlock";
import ProbeSampleCard from "@/components/ProbeSampleCard";
import { Badge, ProbeStatusBadge } from "@/components/StatusBadge";

type Params = { params: { id: string } };

export function generateMetadata({ params }: Params): Metadata {
  const m = getModelById(decodeModelId(params.id));
  return { title: m ? m.name : "模型详情" };
}

export default function ModelDetailPage({ params }: Params) {
  const id = decodeModelId(params.id);
  const model = getModelById(id);

  if (!model) {
    return (
      <ConsoleLayout eyebrow="Model Detail" title="模型未找到">
        <div className="card-surface p-8 text-center">
          <p className="font-mono text-sm text-foreground">{id}</p>
          <p className="mt-3 text-sm text-muted-foreground">
            该 id 不在 web/data/models.json 的 {ALL_MODELS.length} 条真实条目中；页面不展示任何编造模型。
          </p>
          <Link href="/models" className="focus-ring mt-6 inline-flex rounded-md bg-primary px-4 py-2 text-button font-medium text-primary-foreground">
            返回模型广场
          </Link>
        </div>
      </ConsoleLayout>
    );
  }

  const caps = capabilitiesOf(model);
  const provider = providerOf(model.id);
  const related = ALL_MODELS.filter((m) => providerOf(m.id) === provider && m.id !== model.id).slice(0, 6);
  const pricingEntries = Object.entries(model.pricing ?? {}).sort(([a], [b]) => a.localeCompare(b));

  const curl = `curl $NEXT_PUBLIC_BASE/v1/chat/completions \\
  -H "Authorization: Bearer sk-xxxx" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"${model.id}","messages":[{"role":"user","content":"hi"}]}'`;
  const python = `from openai import OpenAI
client = OpenAI(base_url="$NEXT_PUBLIC_BASE/v1", api_key="sk-xxxx")
print(client.chat.completions.create(
    model="${model.id}",
    messages=[{"role": "user", "content": "hi"}],
).choices[0].message.content)`;

  return (
    <ConsoleLayout
      eyebrow={`Model Detail · ${provider}`}
      title={model.name}
      description={model.description}
      actions={
        <Link href="/models" className="focus-ring rounded-md border border-border bg-card px-4 py-2 text-button font-medium text-foreground no-underline transition-colors duration-quick hover:bg-card-hover">
          返回广场
        </Link>
      }
    >
      <div className="mb-6 flex flex-wrap items-center gap-2">
        <Badge tone="info" mono>{model.id}</Badge>
        <Badge tone="neutral" mono>{provider}</Badge>
        <Badge tone="positive">{formatContext(model.context_length)} context</Badge>
        <ProbeStatusBadge status="unavailable" />
      </div>

      {/* 关键数值 */}
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <InfoCard label="上下文长度" value={formatContext(model.context_length)} sub="context_length" />
        <InfoCard label="输入价格 / 1M" value={formatUsd(promptPricePerMtok(model))} sub="pricing.prompt × 1e6" />
        <InfoCard label="输出价格 / 1M" value={formatUsd(completionPricePerMtok(model))} sub="pricing.completion × 1e6" />
        <InfoCard label="最大输出" value={String(model.top_provider?.max_completion_tokens ?? "—")} sub="top_provider.max_completion_tokens" />
      </div>

      {/* architecture 原样展示 */}
      <section className="mt-8">
        <h2 className="mb-4 font-brand text-base font-semibold text-heading">architecture（上游原样）</h2>
        <div className="card-surface grid gap-4 p-5 sm:grid-cols-2">
          <Field label="modality" value={model.architecture?.modality ?? "—"} />
          <Field label="tokenizer" value={model.architecture?.tokenizer ?? "—"} />
          <Field label="instruct_type" value={model.architecture?.instruct_type ?? "—"} />
          <Field label="input_modalities" value={(model.architecture?.input_modalities ?? []).join(", ") || "—"} />
          <Field label="output_modalities" value={(model.architecture?.output_modalities ?? []).join(", ") || "—"} />
          <Field label="top_provider.context_length" value={String(model.top_provider?.context_length ?? "—")} />
          <Field label="top_provider.is_moderated" value={String(model.top_provider?.is_moderated ?? "—")} />
          <Field label="canonical_slug" value={model.canonical_slug ?? "—"} />
          <Field label="created (unix)" value={String(model.created)} />
          <Field label="supported_parameters" value={`${model.supported_parameters.length} 项`} />
        </div>
      </section>

      {/* 能力位 */}
      <section className="mt-8">
        <h2 className="mb-4 font-brand text-base font-semibold text-heading">能力位（由真实字段推导）</h2>
        <div className="flex flex-wrap gap-2">
          {CAPABILITY_LABELS.map((c) => (
            <span key={c.key} className={`rounded-full border px-3 py-1.5 text-xs ${caps[c.key] ? "border-positive/30 bg-positive/8 text-positive-text" : "border-border bg-muted/50 text-muted-foreground/60"}`}>
              {caps[c.key] ? "✓" : "—"} {c.label}
            </span>
          ))}
        </div>
      </section>

      {/* pricing 全字段 */}
      <section className="mt-8">
        <h2 className="mb-4 font-brand text-base font-semibold text-heading">pricing（全部 {pricingEntries.length} 个真实字段）</h2>
        <div className="card-surface overflow-hidden">
          <table className="w-full border-collapse text-left">
            <thead>
              <tr className="border-b border-border bg-muted/40 text-overline uppercase tracking-wider text-muted-foreground">
                <th className="px-5 py-3 font-medium">字段</th>
                <th className="px-5 py-3 font-medium">原值 (USD/token)</th>
                <th className="px-5 py-3 font-medium">换算 (USD/1M)</th>
              </tr>
            </thead>
            <tbody>
              {pricingEntries.map(([k, v]) => (
                <tr key={k} className="border-b border-border/60">
                  <td className="px-5 py-2.5 font-mono text-xs text-foreground">{k}</td>
                  <td className="px-5 py-2.5 font-mono text-xs text-muted-foreground">{String(v)}</td>
                  <td className="px-5 py-2.5 font-mono text-xs tabular-nums text-foreground">
                    {typeof v === "string" || typeof v === "number" ? formatUsd(Number(v) < 0 ? null : Number(v) * 1e6) : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      {/* supported_parameters */}
      <section className="mt-8">
        <h2 className="mb-4 font-brand text-base font-semibold text-heading">supported_parameters（{model.supported_parameters.length} 项）</h2>
        <div className="card-surface flex flex-wrap gap-1.5 p-5">
          {model.supported_parameters.map((p) => (
            <span key={p} className="rounded-sm border border-border bg-muted/60 px-2 py-1 font-mono text-2xs text-muted-foreground">{p}</span>
          ))}
        </div>
      </section>

      {/* 真实探测（F4） */}
      <section className="mt-8">
        <h2 className="mb-4 font-brand text-base font-semibold text-heading">真实输入 / 输出样例与实测延迟 (F4)</h2>
        <ProbeSampleCard modelId={model.id} probe={null} />
      </section>

      {/* 调用示例 */}
      <section className="mt-8">
        <h2 className="mb-4 font-brand text-base font-semibold text-heading">统一出口调用示例</h2>
        <div className="grid gap-4 lg:grid-cols-2">
          <CodeBlock filename="curl · /v1/chat/completions" code={curl} />
          <CodeBlock filename="python · OpenAI SDK" language="python" code={python} />
        </div>
      </section>

      {/* 同供应商真实模型 */}
      {related.length > 0 && (
        <section className="mt-10">
          <h2 className="mb-4 font-brand text-base font-semibold text-heading">同供应商真实模型</h2>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {related.map((m) => (
              <Link key={m.id} href={`/models/${encodeURIComponent(m.id)}`} className="focus-ring rounded-lg border border-border bg-card px-4 py-3 transition-colors duration-quick hover:border-primary/40 hover:bg-card-hover">
                <p className="truncate text-sm font-medium text-heading">{m.name}</p>
                <p className="mt-1 truncate font-mono text-2xs text-muted-foreground">{m.id}</p>
              </Link>
            ))}
          </div>
        </section>
      )}

      <p className="mt-10 font-mono text-2xs text-muted-foreground">
        source: data/models-real.json · 所有字段逐字来自上游 JSON，未做任何补全或编造。
      </p>
    </ConsoleLayout>
  );
}

function InfoCard({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <div className="card-surface p-5">
      <p className="text-overline uppercase tracking-widest text-muted-foreground">{label}</p>
      <p className="mt-3 font-brand text-display font-bold tracking-tight tabular-nums text-heading">{value}</p>
      <p className="mt-1.5 font-mono text-2xs text-muted-foreground">{sub}</p>
    </div>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border border-border bg-muted/30 px-3 py-2">
      <p className="font-mono text-2xs text-muted-foreground">{label}</p>
      <p className="mt-1 break-all font-mono text-xs text-foreground">{value}</p>
    </div>
  );
}
