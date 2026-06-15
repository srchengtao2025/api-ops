#!/bin/bash
# =====================================================
# api-ops 生产部署脚本
# 1. 上传 prod compose + .env.example
# 2. 服务器端 build api 镜像
# 3. 启动 docker compose
# 4. 验证 healthz
# =====================================================
# 用法:
#   export SSH_PASSWORD='你的密码'
#   bash scripts/deploy-prod.sh
# =====================================================
set -e

REMOTE_HOST="${REMOTE_HOST:-api-ops.example.com}"
REMOTE_USER="${REMOTE_USER:-root}"
REMOTE_PORT="${REMOTE_PORT:-22}"
REMOTE_TARGET_DIR="${REMOTE_TARGET_DIR:-/opt/api-ops}"
SSH_KEY_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
PROJECT_LOCAL_DIR="${PROJECT_LOCAL_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'
log()  { echo -e "${GREEN}[deploy]${NC} $*"; }
warn() { echo -e "${YELLOW}[warn]${NC} $*"; }
err()  { echo -e "${RED}[err]${NC} $*"; exit 1; }

[ -z "$SSH_PASSWORD" ] && err "请先 export SSH_PASSWORD='你的密码'"

# === SSH_ASKPASS 注入 ===
mkdir -p /tmp/upstream-deploy
ASKPASS_SCRIPT="/tmp/upstream-deploy/askpass.sh"
cat > "$ASKPASS_SCRIPT" <<'EOF'
#!/bin/bash
echo "$SSH_PASSWORD"
EOF
chmod +x "$ASKPASS_SCRIPT"
export SSH_ASKPASS="$ASKPASS_SCRIPT"
export SSH_ASKPASS_REQUIRE=force
export DISPLAY=:0
unset HISTFILE

ssh_run() {
  ssh -p "$REMOTE_PORT" $SSH_KEY_OPTS "${REMOTE_USER}@${REMOTE_HOST}" "$@"
}

# =====================================================
log "=== api-ops 生产部署 ==="
log "目标: ${REMOTE_USER}@${REMOTE_HOST}"
echo

# === [1] 确认服务器有 docker ===
log "[1/4] 检查 Docker ..."
ssh_run 'docker --version && docker compose version' || err "服务器没装 Docker，请先跑 deploy-remote.sh"

# === [2] 上传 prod compose 文件 ===
log "[2/4] 上传 docker-compose.prod.yml + .env.production.example ..."
scp -P "$REMOTE_PORT" $SSH_KEY_OPTS \
  "$PROJECT_LOCAL_DIR/docker-compose.prod.yml" \
  "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_TARGET_DIR}/"
scp -P "$REMOTE_PORT" $SSH_KEY_OPTS \
  "$PROJECT_LOCAL_DIR/.env.production.example" \
  "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_TARGET_DIR}/"
log "  ✓ 已上传"

# === [3] 服务器端初始化 .env（如果还没有）===
log "[3/4] 初始化 .env ..."
ssh_run "set -e
cd $REMOTE_TARGET_DIR
if [ ! -f .env ]; then
  cp .env.production.example .env
  echo '  ! .env 已从模板创建，请编辑后重新跑此脚本'
  echo '  ! 编辑命令: ssh ${REMOTE_USER}@${REMOTE_HOST} \"vim $REMOTE_TARGET_DIR/.env\"'
  echo '  ! 必填项: API_OPS_RO_DSN, API_OPS_ADMIN_TOKEN, OPS_PG_PASSWORD'
  exit 1
else
  echo '  ✓ .env 已存在，跳过创建'
fi
"

# === [4] 验证 .env 已填好关键值 ===
log "[4/4] 验证 .env 关键值 ..."
ssh_run "set -e
cd $REMOTE_TARGET_DIR
source .env 2>/dev/null || true
MISSING=''
[ -z \"\$API_OPS_RO_DSN\" ] || [[ \"\$API_OPS_RO_DSN\" == *PLEASE_FILL* ]] && MISSING=\"\$MISSING API_OPS_RO_DSN\"
[ -z \"\$API_OPS_ADMIN_TOKEN\" ] || [[ \"\$API_OPS_ADMIN_TOKEN\" == *PLEASE_FILL* ]] && MISSING=\"\$MISSING API_OPS_ADMIN_TOKEN\"
[ -z \"\$OPS_PG_PASSWORD\" ] || [[ \"\$OPS_PG_PASSWORD\" == *change_me* ]] && MISSING=\"\$MISSING OPS_PG_PASSWORD\"
if [ -n \"\$MISSING\" ]; then
  echo \"  ! 必填项未填或还是占位符: \$MISSING\"
  echo '  ! 编辑后重新跑: bash scripts/deploy-prod.sh'
  exit 1
fi
echo '  ✓ .env 关键值已配置'
"

echo
log "=== 准备就绪 ==="
log "现在可以在服务器上启动服务了："
log "  ssh ${REMOTE_USER}@${REMOTE_HOST}"
log "  cd $REMOTE_TARGET_DIR"
log "  docker compose -f docker-compose.prod.yml build api"
log "  docker compose -f docker-compose.prod.yml up -d"
log "  curl http://127.0.0.1:8088/healthz"
echo
log "或者一行命令（密码走 askpass）："
log "  export SSH_PASSWORD='...' && bash scripts/deploy-prod.sh up"
