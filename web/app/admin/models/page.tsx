import type { Metadata } from "next";
import { ALL_MODELS, providerOf } from "@/lib/models";
import ConsoleLayout from "@/components/ConsoleLayout";
import AdminModelTable from "@/components/AdminModelTable";

export const metadata: Metadata = { title: "模型池与别名" };

export default function AdminModelsPage() {
  const rows = ALL_MODELS.slice(0, 40).map((m) => ({
    id: m.id,
    name: m.name,
    provider: providerOf(m.id),
    context: m.context_length,
    prompt: String(m.pricing?.prompt ?? "—"),
  }));

  return (
    <ConsoleLayout
      eyebrow="Admin · F3 / F11"
      title="模型池与别名映射"
      description="合并 / 拆分 / 禁用 / 改名 / 定价覆盖。下表为 data/models-real.json 前 40 条真实模型；alias 默认等于真实 upstream id，编辑经 PUT /api/models/:id 提交。"
    >
      <AdminModelTable rows={rows} />

      <section className="mt-8 grid gap-5 lg:grid-cols-2">
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">别名契约</h3>
          <pre className="mt-3 overflow-x-auto rounded-md border border-border bg-muted/40 p-3 font-mono text-2xs leading-relaxed text-foreground/90">
{`GET  /api/aliases
POST /api/aliases          { alias, canonical_model_id, price_override }
PUT  /api/aliases/:alias
PUT  /api/models/:id       { enabled, alias, price_override }
POST /api/models/:id/probe`}
          </pre>
        </div>
        <div className="card-surface p-5">
          <h3 className="text-sm font-semibold text-heading">参数来源标注</h3>
          <ul className="mt-3 space-y-2 text-xs text-muted-foreground">
            <li><span className="font-mono text-foreground">param_source=upstream</span> · 上游原始 JSON 直接提取</li>
            <li><span className="font-mono text-foreground">param_source=builtin</span> · 内置能力表补全（标注来源）</li>
            <li><span className="font-mono text-foreground">param_source=probe</span> · 真实探测采集</li>
            <li><span className="font-mono text-foreground">probe_status=unavailable</span> · 未采集成功时不填充编造值</li>
          </ul>
        </div>
      </section>

      <p className="mt-8 font-mono text-2xs text-muted-foreground">
        共 {ALL_MODELS.length} 条真实模型；本页展示 {rows.length} 条，完整列表见 /models。
      </p>
    </ConsoleLayout>
  );
}
