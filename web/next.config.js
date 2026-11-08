/** @type {import('next').NextConfig} */
const backend = process.env.LLM_HUB_BACKEND_ORIGIN || "";

const nextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  // 容器化部署：web/Dockerfile 运行层使用 .next/standalone 最小依赖树。
  // 本地 `npm start` 不受影响（standalone 与常规输出可共存）。
  output: "standalone",
  // 前端零第三方后端依赖：未配置后端时不做任何代理，页面完全由静态真实数据集渲染。
  // 部署时设置 LLM_HUB_BACKEND_ORIGIN=http://127.0.0.1:8080 即把 /api 与 /v1 交给 Go 网关。
  async rewrites() {
    if (!backend) return [];
    return [
      { source: "/api/:path*", destination: `${backend}/api/:path*` },
      { source: "/v1/:path*", destination: `${backend}/v1/:path*` },
      { source: "/healthz", destination: `${backend}/healthz` },
      { source: "/metrics", destination: `${backend}/metrics` },
    ];
  },
};

module.exports = nextConfig;
