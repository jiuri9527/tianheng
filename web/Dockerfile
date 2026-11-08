# =============================================================================
# 天衡（Tianheng）前端镜像 —— Next.js 14 App Router 多阶段构建
# 构建：
#   docker build -f web/Dockerfile \
#     --build-arg LLM_HUB_BACKEND_ORIGIN=http://app:8080 \
#     -t tianheng-web ./web
# 说明：Next.js 的 rewrites 在 build 时烘焙进 routes-manifest.json，
#       因此 LLM_HUB_BACKEND_ORIGIN 必须作为 build-arg 传入（运行期再传无效）。
# =============================================================================

# ---------- 依赖层：只装 package.json / lock，命中缓存 ----------
FROM node:20-alpine AS deps
WORKDIR /app
RUN apk add --no-cache libc6-compat
COPY package.json package-lock.json ./
# 生产构建需要 devDependencies（typescript / tailwind / postcss）
RUN npm ci --no-audit --no-fund

# ---------- 构建层 ----------
FROM node:20-alpine AS builder
WORKDIR /app
ENV NEXT_TELEMETRY_DISABLED=1
# 关键：后端地址在构建期固化进 rewrites
ARG LLM_HUB_BACKEND_ORIGIN=http://127.0.0.1:8080
ENV LLM_HUB_BACKEND_ORIGIN=${LLM_HUB_BACKEND_ORIGIN}
# 前端展示用的部署域名（生成 curl / SDK 示例）
ARG NEXT_PUBLIC_DEPLOY_HOST=http://localhost
ENV NEXT_PUBLIC_DEPLOY_HOST=${NEXT_PUBLIC_DEPLOY_HOST}
COPY --from=deps /app/node_modules ./node_modules
COPY . .
# 真实模型数据集必须在构建期存在（web/data/models.json 由 scripts/export-models.mjs 从 data/models-real.json 导出）
RUN test -f data/models.json || (echo "ERROR: web/data/models.json 缺失，请先运行 node scripts/export-models.mjs" && exit 1)
RUN npm run build

# ---------- 运行层：standalone 输出，非 root ----------
FROM node:20-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3000 \
    HOSTNAME=0.0.0.0
RUN addgroup -g 10001 -S nodejs && adduser -S nextjs -u 10001 -G nodejs

# standalone 自带最小依赖树
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static
COPY --from=builder --chown=nextjs:nodejs /app/public ./public
# 真实模型数据（页面静态展示用）
COPY --from=builder --chown=nextjs:nodejs /app/data ./data

USER nextjs
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget -qO- http://127.0.0.1:3000/ >/dev/null 2>&1 || exit 1
CMD ["node", "server.js"]
