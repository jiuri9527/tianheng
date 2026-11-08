import type { Metadata } from "next";
import ConsoleLayout from "@/components/ConsoleLayout";
import ChannelForm from "@/components/ChannelForm";
import AdminRuntime from "@/components/AdminRuntime";

export const metadata: Metadata = { title: "渠道贡献与管理" };

export default function ChannelsPage() {
  return (
    <ConsoleLayout
      eyebrow="Contribute · F2 / F3 / F4 · Auto Discovery"
      title="贡献渠道"
      description="只填 provider_type / base_uri / api_key / visibility。提交后站点自动用该密钥实时请求上游 GET /models，拉取全部真实模型与参数，并对每条做一次真实极小调用采集真实输入/输出样例；用户不需要填写任何模型名。"
    >
      <ChannelForm />

      <section className="mt-10">
        <h2 className="mb-2 font-brand text-base font-semibold text-heading">我的渠道（运行时）</h2>
        <p className="mb-4 text-xs leading-relaxed text-muted-foreground">
          数据源 GET /api/channels；支持列表、编辑、启停、删除、手动同步与探测。后端未连接时展示真实空态，绝不填充编造数据。
        </p>
        <AdminRuntime kind="channels" />
      </section>

      <section className="mt-10">
        <h2 className="mb-2 font-brand text-base font-semibold text-heading">自动发现如何工作</h2>
        <ol className="grid gap-3 text-xs leading-relaxed text-muted-foreground md:grid-cols-3">
          <li className="card-surface p-4">
            <span className="font-mono text-2xs text-primary">01 · 提交</span>
            <p className="mt-2 text-heading">保存 base_uri + api_key</p>
            <p className="mt-1">密钥 AES-256-GCM 加密落库，只回 key_hint；SSRF 校验通过后才出站。</p>
          </li>
          <li className="card-surface p-4">
            <span className="font-mono text-2xs text-primary">02 · 实时发现</span>
            <p className="mt-2 text-heading">GET /models 拉全部真实模型</p>
            <p className="mt-1">OpenAI 兼容 Bearer，Anthropic 用 x-api-key + anthropic-version: 2023-06-01；逐字解析 id/参数/价格/能力位。</p>
          </li>
          <li className="card-surface p-4">
            <span className="font-mono text-2xs text-primary">03 · 真实探测</span>
            <p className="mt-2 text-heading">每条模型一次真实极小调用</p>
            <p className="mt-1">记录真实输入、真实输出、token 用量与延迟；失败只标记状态，绝不编造输出内容。</p>
          </li>
        </ol>
      </section>

      <div className="mt-10 rounded-lg border border-border bg-muted/30 px-5 py-4 font-mono text-2xs leading-relaxed text-muted-foreground">
        POST /api/channels · GET /api/channels/:id · POST /api/channels/:id/sync-models · POST /api/channels/:id/probe · POST /api/channels/:id/test
        <br />
        上游模型列表为空或拉取失败时 models=[] 且 probe_status=unavailable，绝不填充占位/编造模型（SPEC C-1）。
      </div>
    </ConsoleLayout>
  );
}
