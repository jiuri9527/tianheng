// 文件: web/app/monitor/page.tsx
// 实时刷新监控面板（public，无需 JWT）：所有人可见的模型调用实时数据与多条曲线。
import type { Metadata } from "next";
import ConsoleLayout from "@/components/ConsoleLayout";
import MonitorClient from "@/components/MonitorClient";

export const metadata: Metadata = { title: "实时监控 · 天衡" };

export default function MonitorPage() {
  return (
    <ConsoleLayout
      eyebrow="Live Telemetry · public"
      title="实时监控面板"
      description="所有人可见的模型调用实时数据：QPS、成功率、TTFT、token 消耗与按模型/渠道/供应商/状态分布的多条曲线。数据每 2 秒经 SSE 推送、定时轮询兜底；全部来自真实 request_logs 聚合，仅含聚合数字，无数据为真实空态。"
    >
      <MonitorClient />
    </ConsoleLayout>
  );
}
