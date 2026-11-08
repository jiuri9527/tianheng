import type { Metadata } from "next";
import ConsoleLayout from "@/components/ConsoleLayout";
import AdminRuntime from "@/components/AdminRuntime";

export const metadata: Metadata = { title: "渠道管理" };

const CHECKS = [
  "① base_uri 合法性：必须 http(s)://，拒绝 127.0.0.0/8、10/8、172.16/12、192.168/16、169.254.169.254、::1、file/gopher，DNS 解析后二次校验。",
  "② 用该 key 请求上游 GET {base_uri}/models 验证可用；失败则模型列表为空并标记 probe_status=unavailable。",
  "③ api_key AES 加密落库，只返回 key_hint（末 4 位），之后永不明文回显。",
  "④ 手动/定时同步：OpenAI 系 /models、Anthropic /models（x-api-key + anthropic-version）、Gemini 兼容层 /models、自定义 models_path 覆盖。",
  "⑤ 探测：每个新模型发一次极小请求，记录真实输入体、真实输出文本、tokens、latency_ms、成功与否。",
];

export default function AdminChannelsPage() {
  return (
    <ConsoleLayout
      eyebrow="Admin · F2 / F3 / F4"
      title="渠道管理"
      description="渠道 CRUD、一键同步/探测、健康状态。上游模型发现与参数补全全部来自真实请求；未拉取成功时列表为空，不填充占位模型。"
    >
      <AdminRuntime kind="channels" />

      <section className="mt-10">
        <h2 className="mb-4 font-brand text-base font-semibold text-heading">保存前校验链</h2>
        <ol className="space-y-2">
          {CHECKS.map((c, i) => (
            <li key={i} className="card-surface px-5 py-3 text-sm leading-relaxed text-muted-foreground">{c}</li>
          ))}
        </ol>
      </section>

      <p className="mt-8 font-mono text-2xs text-muted-foreground">
        POST /api/channels/:id/test · /sync-models · /probe · 定时健康检查每 60s 用轻量 /models 刷新 status
      </p>
    </ConsoleLayout>
  );
}
