# 同步 SOP: rezeai-ops → api-ops

> **生产领先**模式（2026-06-15 23:00 决策）。本文是 rezeai-ops 内部维护者向 api-ops 公开仓库推 commit 的标准操作流程。

---

## 适用场景

你在生产仓库 `rezeai-ops`（内网 GitLab private）改完一批代码、上线验证后，想把**非敏感的代码改进**同步到公开仓库 `api-ops`（GitHub public），让社区享受。

**不适用**：
- 内部专属功能（如客户专属对账模板、internal-only 工具）
- 含真 IP / token / 密码 / 业务数据的 commit
- 性能调优到 1.9M logs 这种只有内部数据量才能验证的 PR（PR 进去社区跑不动）

---

## 节奏

- **每周一次**，建议周六上午（10:00 - 11:00）
- 推 7 天窗口（`--since="7 days ago"`）
- 一次推 1-3 个 commit，**不要攒一个月再推 30 个**（review 成本爆炸）

---

## 工具准备

```bash
# 1. 两套仓库本地都要有
ls ~/Documents/rezeai-ops/rezeai-ops    # 生产
ls ~/Desktop/api-ops                    # 开源 (snapshot)

# 2. 准备 bot author (api-ops 公开仓库不暴露真实开发邮箱)
cd ~/Desktop/api-ops
git config --local user.name  "api-ops-bot"
git config --local user.email "noreply@api-ops.dev"

# 3. 加 rezeai-ops 作临时 remote (只读 fetch, 不 push)
git remote add rezeai-prod ~/Documents/rezeai-ops/rezeai-ops
git fetch rezeai-prod main

# 4. 准备"已推送 marker" 文件
touch .last-sync-to-open
date '+%Y-%m-%d %H:%M:%S' > .last-sync-to-open
```

---

## 7 步标准流程

### Step 1: 列出候选 commit

```bash
# 改用 rezeai-ops 仓库视角
cd ~/Documents/rezeai-ops/rezeai-ops

# 拉过去 7 天所有 commit (按时间倒序)
git log --since="7 days ago" --pretty=format:"%H %ai %an <%ae> %s" | head -50
```

### Step 2: 逐个过"敏感判定清单"

每个 commit 都过一遍下面 7 条。命中任一 → 标 `[SKIP]` 跳过。

| # | 检查项 | 自动化方法 | 手动 fallback |
|---|---|---|---|
| 1 | 含真 IP / RDS host / ECS 公网 IP / 真域名 | `git show <sha> \| grep -E "47\.251\.\|pgm-[a-z0-9]+\|upstream-pg\.example\.com\|api-ops\.example\.com"` | 肉眼搜 "47.251" "pgm-" "RDS" |
| 2 | 含真 token / 密码 / SSH 凭据 / API key | `git show <sha> \| grep -iE "password=\|token=\|api_key\|secret="` | 肉眼搜 "password" "token" "sk-" |
| 3 | 含真客户名 / 真 vendor / 真模型名 | `git show <sha> \| grep -iE "Phanthy\|dataeyes\|ezmodel\|ccmax\|claudeflare\|aliyun_bailian\|deepseek\|moonshot\|gpt-4o\|claude-3-5-sonnet\|claude-opus"` | 肉眼搜 "Phanthy" "gpt-4o" |
| 4 | 含真业务数字 (revenue / cost / ratio 跟 5095/2753/45.9% 接近) | `git show <sha> \| grep -E "\\\$5095\|\\\$2753\|45\.9%"` | 肉眼搜 "5095" "2753" "45.9" |
| 5 | 含真部署路径 | `git show <sha> \| grep -E "/opt/rezeai-ops\|/data/billing-exports"` | 肉眼搜 "/opt/" "/data/" |
| 6 | commit message 提到具体客户 / 团队成员名字 | `git log -1 --format=%s <sha> \| grep -E "客户\|user\|@"` | 肉眼过 message |
| 7 | 仅适用于内部数据量验证 (e.g. 1.9M logs) | 跟原 commit 作者确认 | review PR description |

**举例**：
```
$ git log --since="7 days ago" --pretty=format:"%H %s"
abc1234 fix(billing/v3): 上游对账 cost 公式补 R1 边界
def5678 docs(DESIGN): 补 Q-C11 错误率新口径决策记录
ghi9012 feat(monitor): 渠道卡片红色发光
jkl3456 fix(deploy): 真 RDS 凭据替换占位符  ← [SKIP] 命中 #2
mno7890 feat(v4): 利润分析模型维度   ← 命中 #4 (用了 45.9% 业务数字), 改占位后再推
```

### Step 3: 改占位词

非敏感的 commit 也可能顺手写了点业务例子（如文档里说"45.9% 毛利率"），需要把数字 / vendor 名替换成占位。

```bash
# 1. checkout 那个 commit 临时改
git checkout -b tmp/sync-abc1234 abc1234

# 2. sed 替换（保留 git history 干净，commit message 改不动，message 提到"45.9%"就 mark skip）
sed -i '' 's|45\.9%|81.9%|g' docs/CHANGELOG.md

# 3. commit "占位"变更
git add -p
git commit -m "fix(sync): 业务数字占位 (45.9% → 81.9%)"
# 注意: 这个 commit 要跟原 commit 分开, 不要 amend

# 4. 回到 api-ops 仓库
cd ~/Desktop/api-ops

# 5. 把 tmp/sync-abc1234 分支的 patch 拿过来
git format-patch -2 rezeai-prod/main..tmp/sync-abc1234
# 生成 0001-xxx.patch + 0002-xxx.patch

# 6. apply (会保留原 commit message)
git am 0001-xxx.patch 0002-xxx.patch

# 7. 清理
rm 0001-xxx.patch 0002-xxx.patch
```

### Step 4: 验证 (1 个 commit 都要做的 5 项)

```bash
# 1. 编译还过
go build -o /tmp/api-ops-build-test ./cmd/server || echo "❌ build fail"
echo "  ✓ build"

# 2. 容器内测试
docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-mod=mod golang:1.22-alpine \
  sh -c "go test ./internal/... 2>&1 | grep -E 'FAIL|ok' | head -10" || echo "❌ test fail"
echo "  ✓ test"

# 3. gitleaks 7 项复查
grep -rln "BEGIN.*PRIVATE KEY\|sk_live_\|47\.251\." --include="*.go" --include="*.md" . && echo "❌ leak" || echo "  ✓ no secret"
echo "  ✓ gitleaks"

# 4. 业务占位覆盖
grep -rE "Phanthy|dataeyes|gpt-4o|45\.9%|5095|2753" --include="*.go" --include="*.md" . && echo "❌ business leak" || echo "  ✓ placeholder ok"
echo "  ✓ no business data"

# 5. gofmt 干净
gofmt -l . | grep -v "^\.git/" && echo "❌ unformatted" || echo "  ✓ formatted"
echo "  ✓ gofmt"
```

### Step 5: 写 sync commit (在 api-ops 仓库加 changelog)

```bash
cd ~/Desktop/api-ops

# 改 docs/CHANGELOG.md, 加新一节 "## [Sync 2026-06-21] from rezeai-ops"
cat >> docs/CHANGELOG.md << 'EOF'

## [Sync 2026-06-21] from rezeai-ops
手动同步本周 rezeai-ops → api-ops 的非敏感 commit:

- abc1234 fix(billing/v3): 上游对账 cost 公式补 R1 边界
- def5678 docs(DESIGN): 补 Q-C11 错误率新口径决策记录
- ghi9012 feat(monitor): 渠道卡片红色发光

跳过 (敏感):
- jkl3456 fix(deploy): 真 RDS 凭据替换占位符 (含真 RDS host)

跳过原因详见 [docs/SYNC-PROD-TO-OPEN.md §敏感判定清单].
EOF

git add docs/CHANGELOG.md
git commit -m "docs(changelog): 同步 2026-06-15 ~ 2026-06-21 rezeai-ops → api-ops"
```

### Step 6: push 到 GitHub

```bash
cd ~/Desktop/api-ops

# 1. 确认 bot author
git log -1 --format='%an <%ae>'
# 期望: api-ops-bot <noreply@api-ops.dev>

# 2. push
git push origin main

# 3. 看 GitHub Actions CI 跑过
#   (你之前 .github/workflows/ci.yml 配的 lint + test + build + docker)
gh run watch
```

### Step 7: 收尾

```bash
# 1. 删 rezeai-ops 临时 remote
cd ~/Desktop/api-ops
git remote remove rezeai-prod

# 2. 删 rezeai-ops tmp 分支
cd ~/Documents/rezeai-ops/rezeai-ops
git branch -D tmp/sync-abc1234

# 3. 在 rezeai-ops 仓库加一行"已同步" marker
cd ~/Documents/rezeai-ops/rezeai-ops
echo "Last sync to api-ops: $(date '+%Y-%m-%d %H:%M')" >> docs/SYNC-LOG.md
git add docs/SYNC-LOG.md
git commit -m "chore(sync): mark 2026-06-21 weekly sync done"

# 4. 写一条 飞书/IM 备忘: "本周 sync 完成, 推了 N 个, 跳过 M 个"
```

---

## 紧急 rollback

万一推上去后发现 commit 含敏感信息：

```bash
# 1. 立即从 GitHub 删 (用 BFG repo-cleaner, 比 git filter-branch 快 10x)
brew install bfg
bfg --delete-files "*.env.local"
bfg --replace-text passwords.txt   # passwords.txt 含要删的字串, 每行一个
git reflog expire --expire=now --all && git gc --prune=now --aggressive
git push --force

# 2. 如果已经有人 clone, 同步发安全公告
#    见 [CONTRIBUTING.md §安全漏洞]
```

**但 7 步流程要尽量避免推到才发现**。Step 4 验证不可省。

---

## 自动化脚本 (TODO)

`scripts/sync-prod-to-open.sh` 待写。计划功能：

- 自动列候选 commit
- 自动跑 7 项敏感判定 (grep)
- 自动 sed 替换（白名单: 业务数字 / vendor / 模型名）
- 输出 `[PUSH] 3 个 / [SKIP] 2 个 / [MANUAL] 1 个` 报告
- 跑完生成 `sync-report-YYYY-MM-DD.md` 进 `docs/`

等 rezeai-ops 仓库首批 sync 完成后写。

---

## 参考 / 链接

- [AGENTS.md §仓库双轨铁律](../AGENTS.md#仓库双轨铁律-2026-06-15-2300-决策-用户拍板) — 决策基线
- [CONTRIBUTING.md §安全漏洞](../CONTRIBUTING.md#安全漏洞--security-issues) — 出事找谁
- [CHANGELOG.md](../CHANGELOG.md) — sync 历史
- [§反向: GitHub PR → rezeai-ops](#反向-github-pr--rezeai-ops-8-步) — 本节, 社区 PR cherry-pick 回生产

---

## 反向: GitHub PR → rezeai-ops (8 步)

> **场景**: 社区用户在 api-ops (GitHub public) 提了一个 PR, 觉得有用, 想 cherry-pick 到生产 rezeai-ops 用上.
>
> **频率**: 期望 ≤ 1 次 / 季度. 99% 情况我们反向推 commit; 反向 cherry-pick 是少数情况.
>
> **决策门槛**: 任何反向 cherry-pick 必先开 issue 讨论, maintainer (你) 拍板才能动.

### 流程图

```
github.com/api-ops/api-ops
       │
       │  PR #N (社区贡献, e.g. feat(billing/v5): quarterly export)
       ▼
1) review PR, 走 api-ops 仓库 merge 标准流程
       │
       │  PR merged, 在 api-ops main 分支
       ▼
2) 在 rezeai-ops 仓库开 issue "Cherry-pick #N from api-ops"
       │
       │  maintainer 拍板
       ▼
3) git fetch 拉 api-ops main 到 rezeai-ops tmp 分支
       │
       │  diff 检查
       ▼
4) 真源等价性验证 (5 项)
       │
       │  ✅ 全部通过
       ▼
5) cherry-pick + 公网 staging 部署
       │
       │  playwright 截图 + 0 console error
       ▼
6) 合并 rezeai-ops main, 部署到 47.251.85.62 公网
       │
       │  飞书告警监控 24h
       ▼
7) 在 api-ops PR 加 comment "Cherry-picked to rezeai-ops main as <sha>, deployed at <timestamp>"
       │
       │  关闭 tracking issue
       ▼
8) 30 天观察期, 出问题 → 立刻回滚 rezeai-ops + 在 api-ops PR 跟 comment
```

### Step 1: review PR, 走 api-ops 标准流程

PR 进来后, 必走 api-ops 仓库的 PR 模板 checklist:

- [ ] RFC 引用 (e.g. `docs/BILLING-v5-RFC.md`)
- [ ] 3 数据源铁律 checkbox (API / RoDB / cache_*)
- [ ] 隐私铁律 checkbox (没真 token / 没真业务数据)
- [ ] CI 全过 (lint + test + build)
- [ ] 至少 1 个 maintainer approve

**特别注意**: 社区 PR 可能用了他们自己的 5 vendor 假名 / 6 模型假名 (跟我们的占位词集一致), 接受. 但**绝不接受**:
- PR 描述里贴真截图 (可能含真 IP / 真客户名)
- PR diff 里含 rezeai-ops 仓库内**才有的** 5 vendor 假名之外的占位词 (e.g. `vendor_zeta` 这种没定义的)
- PR 加了 `cmd/server/seed_admin.go` 改 (这是生产专属脚本, 不该从社区进)

### Step 2: 在 rezeai-ops 仓库开 tracking issue

```bash
cd ~/Documents/rezeai-ops/rezeai-ops

# 用 GitLab CLI 创 issue (或网页)
glab issue create --title "Cherry-pick #N from api-ops: <PR title>" \
  --description "$(cat <<'EOF'
## 来源
- api-ops PR #N: <url>
- api-ops commit: <sha>
- api-ops PR author: <github handle>

## 评估
- [ ] RFC 引用: ...
- [ ] 3 数据源铁律: ...
- [ ] 隐私铁律: ...
- [ ] CI: 全部通过
- [ ] 代码量: <N> 行
- [ ] 影响面: <列出受影响的端点 / SPA / SQL>

## 部署计划
- [ ] cherry-pick 到 rezeai-ops main
- [ ] 部署到 staging (10 分钟)
- [ ] playwright 截图 + 0 console error
- [ ] 部署到 47.251.85.62 公网
- [ ] 24h 监控

## 回滚计划
- [ ] rezeai-ops git revert <sha>
- [ ] 重新部署前一版本 image
- [ ] api-ops PR 加 comment 说明

/maintainer approve 才能继续
EOF
)"
```

### Step 3: 拉 api-ops main 到 tmp 分支

```bash
cd ~/Documents/rezeai-ops/rezeai-ops

# 1. 加 api-ops 作临时 remote (只读 fetch, 不 push)
git remote add api-open ~/Desktop/api-ops
git fetch api-open main

# 2. 看 PR commit 范围
git log api-open/main --oneline | head -10

# 3. 创 tmp 分支 cherry-pick
git checkout -b tmp/cherry-pick-prN api-open/main
```

### Step 4: 真源等价性验证 (5 项)

api-ops 是脱敏镜像, **结构应该跟 rezeai-ops 一样**. 但有 3 个**预期差异** (生产端才有, 镜像里没):

| 差异 | 位置 | 期望 |
|---|---|---|
| `internal/dal/rezeai_cache.go` 类型名 | rezeai-ops 跟 api-ops 一致 (都是 PascalCase) | 0 差异 |
| `cmd/server/seed_admin.go` | rezeai-ops 跟 api-ops 一样 (都是 OPS_ADMIN_BOOTSTRAP_PASSWORD 模式) | 0 差异 |
| `.env` 内容 | rezeai-ops 是真值, api-ops 是 .env.example 占位 | 镜像无 .env, 必然 0 差异 |

**5 项必查**:

```bash
# 1. 文件路径全部一致
diff -r --brief ~/Documents/rezeai-ops/rezeai-ops/ ~/Desktop/api-ops/ | grep -v "^\.git/\|^\.env$\|web/node_modules\|web/dist" | head -20
echo "  (空 = 路径一致 ✅)"

# 2. 占位词覆盖率 (确保 PR 用了 5 vendor 假名集)
grep -rE "Phanthy|dataeyes|ezmodel|ccmax|claudeflare|aliyun_bailian|deepseek|moonshot" tmp/cherry-pick-prN/ 2>&1 | head -5
echo "  (空 = 占位词干净 ✅)"

# 3. 没新加没在占位词集的占位
grep -rE "vendor_[a-z]+|model_[a-z]+|user_[a-z0-9]+" tmp/cherry-pick-prN/ 2>&1 | grep -vE "provider_alpha|provider_beta|provider_gamma|provider_delta|provider_epsilon|llm-model-a|llm-model-a-mini|llm-model-a-pro|llm-model-b|llm-model-b-large|llm-model-c|user_alpha" | head -5
echo "  (空 = 没陌生占位 ✅)"

# 4. 编译
go build -o /tmp/rezeai-build-test ./cmd/server || echo "❌ build fail"
echo "  ✅ build"

# 5. 测试
docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-mod=mod golang:1.22-alpine \
  sh -c "go test ./internal/... 2>&1 | grep -E 'FAIL|^ok' | head -10" || echo "❌ test fail"
echo "  ✅ test"
```

### Step 5: cherry-pick 到 rezeai-ops main

```bash
cd ~/Documents/rezeai-ops/rezeai-ops

# 1. 切回 main
git checkout main

# 2. cherry-pick (可能 1 个 commit, 可能多个, 看 PR 范围)
git cherry-pick api-open/main..tmp/cherry-pick-prN

# 3. 解决冲突 (罕见, 但可能 e.g. CHANGELOG.md 两边都改了)
#    用 --ours / --theirs 策略, 优先 rezeai-ops 版本 (生产优先)

# 4. 改 commit message, 标 source
#    原 message: "feat(billing/v5): 季度对账导出"
#    新 message: "feat(billing/v5): 季度对账导出 (cherry-pick from api-ops #N <sha>)"
```

### Step 6: staging 部署 + playwright 验

```bash
# 1. 部署到 staging (跟生产同 image, 端口 8089, 不同 domain)
docker buildx build --platform linux/amd64 -t rezeai-ops:staging . --load
docker save rezeai-ops:staging | gzip > /tmp/rezeai-staging.tar.gz
sshpass -p '<pwd>' scp /tmp/rezeai-staging.tar.gz root@47.251.85.62:/tmp/
ssh root@47.251.85.62 'docker load -i /tmp/rezeai-staging.tar.gz && \
  docker compose -f docker-compose.staging.yml up -d api'

# 2. playwright 截图 + 0 console error
playwright-mcp screenshot http://47.251.85.62:8089/ --output=/tmp/staging.png
# 肉眼对 5 个页面: dashboard / customers / upstream / v4 / monitor
# console.error 必 0 条

# 3. 跑 curl 200 测受影响的端点
ssh root@47.251.85.62 'docker exec rezeai-ops-api-staging \
  curl -s -o /dev/null -w "%{http_code}\n" \
  http://localhost:8088/api/billing/v5/quarterly-overview'
# 期望 200
```

### Step 7: 部署到公网 + 飞书告警

```bash
# 1. 推 rezeai-ops main (本地不直接 push, 走内网 GitLab)
git push origin main

# 2. CI (内网 GitLab runner) 自动 build + 部署
# 跟正常生产部署一样, 但这次是 cherry-pick 引入的代码

# 3. 部署后 24h 监控
# - 飞书机器人: "Cherry-pick #N deployed at <ts>, monitoring 24h"
# - 重点看: 新代码的端点 latency / 错误率 / DB 慢查询

# 4. 在 api-ops PR 加 comment
gh pr comment N --body "Cherry-picked to rezeai-ops main as <sha>, deployed at <ts>. 24h 监控中. 出问题会回滚并在此 comment."

# 5. 关闭 GitLab issue
glab issue close <issue-id> --comment "Cherry-pick #N done, deployed at <ts>, monitoring 24h"
```

### Step 8: 30 天观察 + 回滚预案

```bash
# 1. 每天看一次新代码的:
#    - 端点 P95 / P99 latency
#    - 端点 4xx / 5xx 错误率
#    - DB 慢查询日志
#    - 内存 / CPU 占用

# 2. 出问题立刻回滚:
cd ~/Documents/rezeai-ops/rezeai-ops
git revert <cherry-pick-sha>
git push origin main
# CI 自动回滚部署

# 3. 在 api-ops PR 跟 comment
gh pr comment N --body "Reverted at <ts> due to <reason>. Issue: <gitlab-issue-url>"

# 4. 30 天后无问题, 在 PR 加 final comment "✅ Cherry-pick stable for 30 days in production"
gh pr comment N --body "✅ Cherry-pick stable for 30 days in production. 永久接受."
```

### 反向 cherry-pick 5 条铁律

1. **必走 issue** — 没 tracking issue 不动手. 例外: 紧急安全补丁 (但仍需 issue 追溯)
2. **必做 5 项验证** — Step 4 的 5 项不省, 任一失败 = close PR + 跟社区用户解释
3. **必 staging** — 跳过 staging 直接上公网 = 高风险, 不允许 (除紧急)
4. **必 24h 监控** — 飞书告警配 "cherry-pick" 标签, 重点盯
5. **必 30 天观察** — 不在 30 天后做 final comment = cherry-pick 流程没闭环

### 反向 cherry-pick 反模式 (禁止)

- ❌ 直接 `git pull api-open main` 到 rezeai-ops (会把脱敏改动一并拉过来)
- ❌ 在 rezeai-ops 仓库 `git remote add` 完不删, 留下 "公开仓库 remote" 历史痕迹
- ❌ cherry-pick 含 `seed_admin.go` 改的 PR (生产专属脚本)
- ❌ cherry-pick 含新 `migrations/*.sql` 不先在 staging 跑过
- ❌ cherry-pick 完不更新 `docs/CHANGELOG.md` (生产 CHANGELOG 跟开源 CHANGELOG 必同步)
- ❌ cherry-pick 完不在飞书告警标签里加 "cherry-pick" 关键字, 30 天后找不到上下文

---

## 自动化脚本 (TODO)

`scripts/sync-prod-to-open.sh` 待写. 计划功能:

- 自动列候选 commit
- 自动跑 7 项敏感判定 (grep)
- 自动 sed 替换 (白名单: 业务数字 / vendor / 模型名)
- 输出 `[PUSH] 3 个 / [SKIP] 2 个 / [MANUAL] 1 个` 报告
- 跑完生成 `sync-report-YYYY-MM-DD.md` 进 `docs/`

反向 cherry-pick 的 `scripts/cherry-pick-from-open.sh` 也待写, 跟上面同一个 PR 写完. 需求:

- 输入 PR 号
- 自动 fetch api-open main
- 自动跑 5 项验证
- 输出报告
- 手工确认后 cherry-pick

等 rezeai-ops 仓库首批正向 sync + 反向 cherry-pick 跑过后再写.
