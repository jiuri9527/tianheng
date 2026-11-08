// 文件: web/components/MonitorTypes.tsx
// 与后端 src/internal/telemetry/telemetry.go 的 Snapshot/GroupStat/Bucket 一一对应。
// 所有字段均为「聚合数字」或「分类键」，绝不含完整输入输出 / 密钥 / 令牌。

export interface MonitorGroupStat {
  key: string;
  requests: number;
  success: number;
  errors: number;
  success_rate: number;
  avg_ttft_ms: number;
  prompt_tokens: number;
  completion_tokens: number;
}

export interface MonitorGroups {
  by_model: MonitorGroupStat[];
  by_channel: MonitorGroupStat[];
  by_provider: MonitorGroupStat[];
  by_status: MonitorGroupStat[];
}

export interface MonitorBucket {
  minute_start: number;
  label: string;
  requests: number;
  success: number;
  errors: number;
  qps: number;
  success_rate: number;
  avg_ttft_ms: number;
  prompt_tokens: number;
  completion_tokens: number;
}

export interface TelemetrySnapshot {
  available: boolean;
  source: string;
  generated_at: number;
  window_seconds: number;
  bucket_seconds: number;
  qps_window_seconds: number;
  total_requests: number;
  qps: number;
  peak_qps: number;
  success_rate: number;
  success_count: number;
  error_count: number;
  avg_ttft_ms: number;
  max_ttft_ms: number;
  ttft_sample_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  stream_requests: number;
  groups: MonitorGroups;
  series: MonitorBucket[];
}

/** 后端不可达时的真实空态（不是编造数据）。 */
export const EMPTY_SNAPSHOT: TelemetrySnapshot = {
  available: false,
  source: "",
  generated_at: 0,
  window_seconds: 3600,
  bucket_seconds: 60,
  qps_window_seconds: 60,
  total_requests: 0,
  qps: 0,
  peak_qps: 0,
  success_rate: 0,
  success_count: 0,
  error_count: 0,
  avg_ttft_ms: 0,
  max_ttft_ms: 0,
  ttft_sample_count: 0,
  prompt_tokens: 0,
  completion_tokens: 0,
  total_tokens: 0,
  stream_requests: 0,
  groups: { by_model: [], by_channel: [], by_provider: [], by_status: [] },
  series: [],
};
