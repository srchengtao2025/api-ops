# 多阶段构建 api-ops（server + seed 一体）

FROM golang:1.22-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/api-ops-server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata postgresql-client && \
    addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=builder /out/api-ops-server /app/api-ops-server
# web/dist 由 CI / 本地 prebuild 后 COPY 进去 (不入 git 库)
# 如果 dist 缺失, server.go 的 NoRoute 会 fallback 到空 SPA
COPY web/dist/ /app/web/dist/
# BILLING v2 账单 HTML 模板 (PR #8 / 8, 2026-06-14)
COPY internal/billing/templates/ /app/internal/billing/templates/
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh
USER app
EXPOSE 8088
ENV TZ=Asia/Shanghai
ENTRYPOINT ["/app/docker-entrypoint.sh"]
