# llm-hub 服务端镜像 —— 零第三方依赖，Go 1.22 标准库自研
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY kernel/ ./kernel/
COPY src/ ./src/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/llm-hub ./src/cmd/server

FROM alpine:3.20
RUN adduser -D -u 10001 app \
 && mkdir -p /app/data /app/state /app/logs \
 && chown -R app:app /app
WORKDIR /app
COPY --from=build /out/llm-hub /app/llm-hub
COPY data/models-real.json /app/data/models-real.json
COPY deploy/.env.example /app/.env.example
USER app
EXPOSE 8080
ENV SERVER_ADDR=:8080 \
    MODELS_REAL_PATH=/app/data/models-real.json \
    STATE_DIR=/app/state
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/app/llm-hub"]
