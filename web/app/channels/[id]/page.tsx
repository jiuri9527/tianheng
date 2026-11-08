import Link from "next/link";
import type { Metadata } from "next";
import ConsoleLayout from "@/components/ConsoleLayout";
import AutoDiscoveryPanel from "../AutoDiscoveryPanel";

type Params = { params: { id: string } };

export function generateMetadata({ params }: Params): Metadata {
  return { title: `渠道详情 · ${decodeURIComponent(params.id)}` };
}

/**
 * 渠道详情页：只展示该渠道通过真实上游 GET /models 自动发现的模型。
 * 不再使用静态数据集做「预览」——未拉取成功时由 AutoDiscoveryPanel 渲染
 * probe_status=unavailable 真实空态，绝不填充编造模型或样例。
 */
export default function ChannelDetailPage({ params }: Params) {
  const channelId = decodeURIComponent(params.id);

  return (
    <ConsoleLayout
      eyebrow="Channel Detail · F3 / F4 · Auto Discovery"
      title={`渠道详情 · ${channelId}`}
      description="站点用该渠道的 api_key 实时请求上游 GET /models，自动发现全部真实模型与参数，并对每条做一次真实极小调用采集真实输入/输出样例。用户不需要填写任何模型名；未拉取成功时列表为空并标记 probe_status=unavailable。"
      actions={
        <Link href="/channels" className="focus-ring rounded-md border border-border bg-card px-4 py-2 text-button font-medium text-foreground no-underline transition-colors duration-quick hover:bg-card-hover">
          返回渠道列表
        </Link>
      }
    >
      <AutoDiscoveryPanel channelId={channelId} title="自动发现的真实模型" />

      <div className="mt-6 rounded-lg border border-border bg-muted/30 px-5 py-4 font-mono text-2xs leading-relaxed text-muted-foreground">
        GET /api/channels/:id · POST /api/channels/:id/sync-models · POST /api/channels/:id/probe · POST /api/channels/:id/test
        <br />
        模型 id / context_length / pricing / architecture / supported_parameters / top_provider 逐字来自真实上游响应；
        输入/输出样例来自真实探测调用。上游不可用时 models=[] 且 probe_status=unavailable（SPEC C-1）。
      </div>
    </ConsoleLayout>
  );
}
