#!/bin/sh
# api-ops 容器入口：默认不跑 seed（生产环境不需要 mock 数据）
# 设 SEED_ON_START=true 才跑（仅 demo / 集成测试场景）
set -e

OPS_DB_DSN_HOST=$(echo "$OPS_DB_DSN" | sed -nE 's/host=([^ ]+).*/\1/p')
SEED_ON_START=${SEED_ON_START:-false}
SEED_SCALE=${SEED_SCALE:-small}

if [ "$SEED_ON_START" = "true" ]; then
  echo "[entrypoint] SEED_ON_START=true, waiting for postgres at $OPS_DB_DSN_HOST:5432 ..."
  for i in $(seq 1 30); do
    if pg_isready -h "$OPS_DB_DSN_HOST" -U api_ops -d api_ops >/dev/null 2>&1; then
      echo "[entrypoint] postgres ready (try $i)"
      break
    fi
    sleep 1
  done

  if [ -x /app/api-ops-seed ]; then
    echo "[entrypoint] running seed (scale=$SEED_SCALE) ..."
    /app/api-ops-seed --scale="$SEED_SCALE" || echo "[entrypoint] seed failed (continuing, server may still start)"
  else
    echo "[entrypoint] SEED_ON_START=true 但 api-ops-seed 不存在 (生产镜像不含), 跳过 seed"
  fi
fi

echo "[entrypoint] starting api-ops server ..."
exec /app/api-ops-server
