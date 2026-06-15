#!/bin/bash
# =====================================================
# api-ops 远程部署脚本 v3 (SSH_ASKPASS 方式)
# 不需要 expect、不消耗 pty、密码用临时环境变量
# 适合慢速网络（阿里云等小水管）
# =====================================================
# 用法:
#   export SSH_PASSWORD='你的密码'
#   bash scripts/deploy-remote.sh
# 可覆盖的 env:
#   REMOTE_HOST       (默认 api-ops.example.com)
#   REMOTE_USER       (默认 root)
#   REMOTE_PORT       (默认 22)
#   REMOTE_TARGET_DIR (默认 /opt/api-ops)
# =====================================================
set -e

# === 配置 ===
REMOTE_HOST="${REMOTE_HOST:-api-ops.example.com}"
REMOTE_USER="${REMOTE_USER:-root}"
REMOTE_PORT="${REMOTE_PORT:-22}"
REMOTE_TARGET_DIR="${REMOTE_TARGET_DIR:-/opt/api-ops}"
PROJECT_LOCAL_DIR="${PROJECT_LOCAL_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
SSH_KEY_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"

# === 颜色 ===
GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'
log()  { echo -e "${GREEN}[deploy]${NC} $*"; }
warn() { echo -e "${YELLOW}[warn]${NC} $*"; }
err()  { echo -e "${RED}[err]${NC} $*"; exit 1; }

# === 校验 ===
[ -z "$SSH_PASSWORD" ] && err "请先 export SSH_PASSWORD='你的密码'"
[ ! -d "$PROJECT_LOCAL_DIR/.git" ] && err "$PROJECT_LOCAL_DIR 不是 git 仓库"

# === 准备 SSH_ASKPASS（密码走 askpass，不占 pty）===
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

# 封装：运行远程命令
# 注意：必须加 -tt，否则 macOS 上 SSH_ASKPASS 不会被读，密码注入失败
ssh_run() {
  ssh -tt -p "$REMOTE_PORT" $SSH_KEY_OPTS "${REMOTE_USER}@${REMOTE_HOST}" "$@"
}

# 封装：scp 到远程
scp_to() {
  scp -P "$REMOTE_PORT" $SSH_KEY_OPTS "$1" "${REMOTE_USER}@${REMOTE_HOST}:$2"
}

# =====================================================
echo
log "=== api-ops 远程部署开始 ==="
log "目标服务器: ${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_PORT}"
log "目标目录:   $REMOTE_TARGET_DIR"
log "本地项目:   $PROJECT_LOCAL_DIR"
echo

# === [1] 测试连通性 ===
log "[1/5] 测试 SSH 连通性 ..."
ssh_run 'echo "  ✓ 已连接 $(uname -srm)"; which git && git --version' || err "SSH 连不上"

# === [2] 准备本地 tar 包（排除大文件/资源叉）===
log "[2/5] 打包本地项目（排除大文件/资源叉）..."
TARBALL="/tmp/upstream-deploy/api-ops.tar.gz"
# macOS 排除 ._* 资源叉；排除 seed 二进制、bin/、node_modules
tar czf "$TARBALL" \
  --exclude='._*' \
  --exclude='.git/objects/pack' \
  --exclude='.git/hooks' \
  --exclude='seed' \
  --exclude='bin' \
  --exclude='web/node_modules' \
  --exclude='web/dist' \
  --exclude='*.log' \
  -C "$PROJECT_LOCAL_DIR" \
  . 2>/dev/null
TARSIZE=$(du -sh "$TARBALL" | cut -f1)
log "  ✓ 已生成 $TARSIZE tar.gz"

# === [3] scp 上传 ===
log "[3/5] 上传 $TARSIZE 到服务器 ..."
time scp_to "$TARBALL" "/tmp/api-ops.tar.gz"
log "  ✓ 上传完成"

# === [4] 服务器上解压、修复属主、清理 macOS 资源叉 ===
log "[4/5] 在服务器上解压 ..."
ssh_run "set -e
rm -rf $REMOTE_TARGET_DIR
mkdir -p $REMOTE_TARGET_DIR
cd $REMOTE_TARGET_DIR
tar xzf /tmp/api-ops.tar.gz
rm -f /tmp/api-ops.tar.gz
chown -R root:root $REMOTE_TARGET_DIR
find $REMOTE_TARGET_DIR -name '._*' -type f -delete 2>/dev/null
git config --global --add safe.directory $REMOTE_TARGET_DIR
echo '  ✓ 解压完成'
"

# === [5] 验证 ===
log "[5/5] 验证服务器状态 ..."
ssh_run 'set -e
cd '"$REMOTE_TARGET_DIR"'
echo "  - git log:"
git log --oneline | sed "s/^/      /"
echo "  - branch:"
git branch --show-current | sed "s/^/      /"
echo "  - 顶层目录:"
ls -la | head -15 | sed "s/^/      /"
SZ=$(du -sh . | cut -f1)
FC=$(find . -type f -not -path "./.git/*" | wc -l)
echo "  - 大小: $SZ"
echo "  - 文件数(不含 .git): $FC"
echo "  - .git 完整性:"
git fsck --no-progress 2>&1 | head -3 | sed "s/^/      /" || true
'

echo
log "=== 部署完成 ==="
log "服务器路径: $REMOTE_TARGET_DIR"
log "下一步:"
log "  ssh ${REMOTE_USER}@${REMOTE_HOST}   # 进入服务器"
log "  cd $REMOTE_TARGET_DIR"
log "  docker compose up -d                # 启动服务"
