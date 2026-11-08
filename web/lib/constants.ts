import type { Capabilities } from "@/lib/models";

/**
 * 轻量常量：供客户端组件（FilterBar 等）引用，避免把整个
 * web/data/models.json 打包进浏览器 bundle。
 */
export const CAPABILITY_LABELS: { key: keyof Capabilities; label: string }[] = [
  { key: "vision", label: "视觉输入" },
  { key: "tools", label: "工具调用" },
  { key: "reasoning", label: "推理" },
  { key: "structuredOutputs", label: "结构化输出" },
  { key: "caching", label: "Prompt 缓存" },
  { key: "file", label: "文件输入" },
  { key: "audio", label: "音频" },
  { key: "video", label: "视频" },
];
