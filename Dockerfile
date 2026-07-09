# 多阶段构建 api-ops（server + seed 一体）

FROM golang:1.22-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/rezeai-ops-server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata postgresql-client && \
    addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=builder /out/rezeai-ops-server /app/rezeai-ops-server
# web/dist 由 CI / 本地 prebuild 后 COPY 进去 (不入 git 库)
# 如果 dist 缺失, server.go 的 NoRoute 会 fallback 到空 SPA
COPY web/dist/ /app/web/dist/
# BILLING v2 账单 HTML 模板 (PR #8 / 8, 2026-06-14)
COPY internal/billing/templates/ /app/internal/billing/templates/
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh
# 客户健康度模块 (2026-07-09, api-ops 同步 rezeai-ops):
#   预先建导出目录, app user 拥有 (跟 /data/billing-exports 一致)
#   父目录 /data 是 root 拥有, app user 没法 mkdir 子目录, 必须 build 时建好
RUN mkdir -p /data/customer-health-exports /data/billing-exports && chown -R app:app /data
USER app
EXPOSE 8088
ENV TZ=Asia/Shanghai
ENTRYPOINT ["/app/docker-entrypoint.sh"]
