# v3 上游对账 ReferenceError 修复 · 报告

**PR**: v3 上游对账 SPA JSX ReferenceError 修复 + AGENTS.md 部署铁律 #10
**日期**: 2026-06-15 08:58 (commit 时间)
**Commit**: `62851ad`
**状态**: ✅ **PASS** (0 exception + 0 console error + 5 vendor 行真实数据)

---

## TL;DR

v3 上游对账 SPA (BillingV3Upstream.tsx line 285) 写错: 描述文字用 `{task_id}` JSX 插值, 但 `task_id` 不在作用域 → 渲染时抛 `ReferenceError: task_id is not defined` → 整个组件崩 → 整页空白. 修复: 改成 HTML 实体 `<taskID>`. 加 AGENTS.md 部署铁律 #10.

| 检查项 | 状态 | 详情 |
|---|---|---|
| 修复前现象 | ✅ | ReferenceError: task_id is not defined |
| 修复 | ✅ | `<taskID>` HTML 实体 (line 285) |
| 二次犯错 | ✅ | 修时又写 `{genVendor}-{ts}.zip`, ts 同样未定义崩第二次 |
| 公网验证 | ✅ | 0 exception + 0 console error |
| 5 vendor 行真实数据 | ✅ | cost $2787 / revenue $5160 |
| AGENTS.md 铁律 #10 | ✅ | JSX `{xxx}` 永远求值, 描述/示例严禁用 `{xxx}` |
| 截图 | ✅ | `docs/screenshots/2026-06-15-v3-final.png` (141 KB) |

---

## 改动清单 (git show --stat)

```
AGENTS.md                                |   1 +
docs/CHANGELOG.md                        |  35 +++++++++++++++++++++++++++++++
docs/screenshots/2026-06-15-v3-final.png | Bin 0 -> 141392 bytes
web/src/pages/BillingV3Upstream.tsx      |   2 +-
4 files changed, 37 insertions(+), 1 deletion(-)
```

---

## Bug 详情

### 原代码 (错)

`web/src/pages/BillingV3Upstream.tsx` line 285:

```jsx
<li>输出: /data/billing-exports/{task_id}.zip</li>
```

### 问题

`{task_id}` 是 JSX 插值表达式, 但 `task_id` 变量在作用域**不存在** → React 渲染时抛:

```
ReferenceError: task_id is not defined
```

→ 整个组件崩 → 整个 v3 上游对账页面空白.

### 二次犯错

第一次修时, 写成:

```jsx
<li>{genVendor}-{ts}.zip</li>
```

`ts` 同样未定义 → 又崩一次.

**根因**: 描述/示例文字想用"模板字符串"风格, 但 JSX 里 `{xxx}` **永远被求值**, 编译器不知道"这是描述文字".

### 修复

`web/src/pages/BillingV3Upstream.tsx` line 285 (验证):

```jsx
<li>输出: /data/billing-exports/<taskID>.zip (复用 v2 任务 ID 命名规则)</li>
```

实际 zip 文件名由后端 `taskID.zip` 决定 (`internal/billing/upstream_format.go` line 253), 不需要前端描述.

---

## 修法 (3 选 1)

| 写法 | 示例 | 适用 |
|---|---|---|
| HTML 实体 | `<taskID>` | 文档/示例说明 (本次选用) |
| shell 风格 | `$(task_id)` | 命令行示例 |
| 直接不带花括号 | `taskid` | 口语化描述 |

---

## AGENTS.md 部署铁律 #10 (新加)

**JSX `{xxx}` 永远求值, 描述/示例文字严禁用 `{xxx}`** —— `<li>输出: /data/{task_id}.zip</li>` 这种"示例代码"在 JSX 里 `{task_id}` 会被求值, `task_id` 不在作用域就 ReferenceError → 组件崩 → 整页空白. 描述/示例用 HTML 实体 `<taskID>` 或 shell 风格 `$(task_id)` 或直接不带花括号.

**实战**: v3 line 285 `{task_id}` 崩 1 次, 修时又写 `{genVendor}-{ts}.zip` 崩 1 次, 共 2 次返工.

**推广到全栈**: 任何 PR 写示例代码/路径/命令, 都**不用** `{xxx}` 格式. 包括:
- API 文档示例 (`/api/billing/v2/.../{task_id}`) — 用 `<task_id>` 或 `[task_id]`
- 部署命令 (`scp <local> root@<host>:<remote>`) — 用 `<host>` 不是 `{host}`
- 代码注释 (`// 写 /data/{path}`) — 注释不在 JSX 里 OK, 但描述路径时建议统一风格
- README + CHANGELOG — 一律 `<xxx>` 风格

**验收标准 (PR 评审加这条)**:
- [ ] PR 改了任何 TSX/JSX 文件, diff 里**不能**有 `\{[a-zA-Z_]+\}` (描述文字场景), 必须用 `<xxx>` / `$(xxx)` / 不带花括号
- [ ] PR 改了任何 TSX/JSX 文件, playwright 截图 0 console error 是**必备**证据

---

## 公网验证 (token 007f4c03...)

### 修复前 (现象)

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/billing/v3/upstream/current-month-overview"
# HTTP 200 (API 正常, 但前端白屏)
```

打开 `https://api-ops.example.com:8091/billing/v3/upstream` 页面 → 白屏, 控制台:

```
Uncaught ReferenceError: task_id is not defined
  at BillingV3Upstream (BillingV3Upstream.tsx:285)
```

### 修复后 (验证)

```bash
# playwright 截图 + console 抓错误
playwright-cli --url "http://api-ops.example.com:8091/billing/v3/upstream" \
  --screenshot /tmp/v3-final.png \
  --check-console-error
# 0 exception + 0 console error ✅
```

v3 表格 5 vendor 行 + 真实数字:
- provider_beta: 1 调用 $0.0008
- provider_gamma: 495 调用 $1.45 / $0.82 成本 / 76.6% 利润率
- provider_alpha / provider_c / provider_d
- **合计: cost $2787 / revenue $5160**

---

## 截图

`docs/screenshots/2026-06-15-v3-final.png` (141 KB, 5 vendor 行表格 + 真实数据)

---

## 关键经验

1. **JSX `{xxx}` 永远求值** — 描述/示例文字严禁 `{xxx}` 格式, 用 `<xxx>` 或 `$(xxx)` 或裸
2. **示例代码不是真的代码** — 编译器不知道"这是描述", 写错就崩
3. **二次犯错要警觉** — 修 1 个 ReferenceError 后, 写新代码别再踩同一个坑
4. **playwright 0 console 是必备** — HTML 200 不够, 一定要浏览器实测 (本 PR 的 Bug 就是 SPA 红屏)
5. **推广到全栈** — API 文档 / 部署命令 / README 全部统一 `<xxx>` 风格

---

## 关联

- AGENTS.md 部署铁律 #10 (本 PR 加)
- AGENTS.md "v3 ReferenceError 历史" 章节 (详细记录)
- v3 PR #5 (`539a82e`) 是这次 Bug 的源头 PR
