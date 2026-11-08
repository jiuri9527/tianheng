import type { Metadata } from "next";
import ConsoleLayout from "@/components/ConsoleLayout";
import AdminRuntime from "@/components/AdminRuntime";

export const metadata: Metadata = { title: "系统设置" };

export default function AdminSettingsPage() {
  return (
    <ConsoleLayout
      eyebrow="Admin · F11"
      title="系统设置"
      description="默认超时、failover 次数、熔断冷却、同步 cron、健康检查间隔、奖励比例、注册开关、脱敏开关与请求体上限。天衡不设任何使用限制（无 RPM/TPM/并发/配额），因此没有限流默认值可配。"
    >
      <AdminRuntime kind="settings" />

      <p className="mt-8 font-mono text-2xs text-muted-foreground">
        GET/PUT /api/admin/settings · settings(key PK, value JSONB, updated_at)
      </p>
    </ConsoleLayout>
  );
}
