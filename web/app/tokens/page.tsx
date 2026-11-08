import type { Metadata } from "next";
import { ALL_MODELS, FEATURED_MODEL_IDS } from "@/lib/models";
import ConsoleLayout from "@/components/ConsoleLayout";
import TokenManager from "@/components/TokenManager";

export const metadata: Metadata = { title: "平台令牌" };

export default function TokensPage() {
  const modelOptions = ALL_MODELS.map((m) => ({ id: m.id, name: m.name }));
  const exampleModelId = FEATURED_MODEL_IDS[0];
  const deployHost = process.env.NEXT_PUBLIC_DEPLOY_HOST ?? "[DEPLOY_HOST]";

  return (
    <ConsoleLayout
      eyebrow="Tokens · F9 / F12"
      title="平台令牌"
      description="创建 sk-<32位随机> 平台令牌。天衡不设任何使用限制：无模型白名单、无 RPM/TPM、无并发上限、无配额、无 IP 白名单、无过期时间，所有令牌可调用全部真实模型。创建后一次性显示明文，并自动生成 curl / Python(OpenAI SDK) / Anthropic SDK 调用示例。"
    >
      <TokenManager
        modelOptions={modelOptions}
        exampleModelId={exampleModelId}
        deployHost={deployHost}
      />
    </ConsoleLayout>
  );
}
