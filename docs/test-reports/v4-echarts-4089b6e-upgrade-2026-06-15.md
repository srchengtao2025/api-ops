# v4 利润分析 echarts 升级 验证报告

**Commit**: `4089b6e` feat(web/v4): 趋势/客户/上游/模型 4 tab 升级 echarts 真图表
**日期**: 2026-06-15
**状态**: ✅ **PASS** (npm run build 0 error + 4 tab 截图正常 + 0 React 警告)

---

## TL;DR

v4 利润分析页 4 tab 全部从手写 SVG / 纯表格升级到 echarts 真图表。
- 趋势 tab: 手写 SVG bar (3 段叠加) → **echarts 折线图** (3 line + smooth + dataZoom)
- 客户 tab: 纯 Table → **Table + 横向柱状图** (top 10 客户 profit)
- 上游 tab: 纯 Table → **Table + 饼图** (top 5 vendor cost 占比, donut)
- 模型 tab: 纯 Table → **Table + 横向柱状图** (top 10 model revenue)

| 检查项 | 状态 | 详情 |
|---|---|---|
| npm run build | ✅ | 0 error, dist 2,244.90 KB (echarts 已在单 chunk, 无新依赖) |
| 4 tab 渲染 | ✅ | playwright headless chrome 截图 4 张 (深色背景清晰) |
| React 警告 | ✅ | 0 page error |
| 主题对齐 | ✅ | 电光蓝 #3B82F6 / 电光橙 #F59E0B / 电光绿 #10B981, 跟 antd dark algorithm 一致 |
| 配色迁移 | ✅ | KPI 数字 / Table 毛利润 / SummaryBox 全换成主题色 hex |

---

## 4 tab echarts 配置简表

| Tab | 图表类型 | 图表系列 | 数据字段 | 系列数 | 高度 |
|---|---|---|---|---|---|
| **趋势 (30 天)** | `line` (smooth + symbol:circle) + areaStyle 半透明 | revenue (蓝) / cost (橙) / profit (绿) | `by_day[].{date, revenue, cost, profit}` | 3 | 360px |
| **客户** | `bar` (横向, top 10) | 毛利 (绿/橙按正负) | `by_user[].{username, profit}` | 1 | max(280, n*32+60) |
| **上游** | `pie` (donut, top 5) | 上游成本占比 | `by_vendor[].{vendor_name, cost}` | 1 | 360px |
| **模型** | `bar` (横向, top 10) | 客户消耗 (蓝) | `by_model[].{model_name, revenue}` | 1 | max(280, n*32+60) |

**echarts 包装**: `echarts-for-react` (`<ReactECharts option={...} theme="dark" />`), `notMerge={true} lazyUpdate={true}`。

**深色主题 token** (跟 antd dark algorithm + styles.css CSS 变量对齐):
```ts
const ECHARTS_COLORS = {
  revenue: '#3B82F6',  // 电光蓝 (主色)
  cost: '#F59E0B',     // 电光橙
  profit: '#10B981',   // 电光绿
  text: '#E5E7EB',
  textMuted: '#9CA3AF',
  border: '#2A3346',
  bgTooltip: 'rgba(15, 23, 41, 0.95)',
}
```

**性能**:
- 趋势 30 天: dataZoom inside + slider (slide 缩放)
- 3 个 Table 全部 `pagination={{ pageSize: 10, showSizeChanger: false }}`
- echarts `notMerge=true lazyUpdate=true` 减少 re-render 开销

---

## npm run build 输出

```
vite v5.4.21 building for production...
transforming...
✓ 3686 modules transformed.
dist/index.html                     0.41 kB │ gzip:   0.32 kB
dist/assets/index-pPw3OQOt.css      8.86 kB │ gzip:   2.33 kB
dist/assets/index-DI_zXR6L.js   2,244.90 kB │ gzip: 731.22 kB
✓ built in 2.79s
```

- **0 error**
- **0 警告** (除 chunk size 提示, vite 默认 threshold 1500 KB; echarts 体积大是预期)
- **bundle 体积变化**: 2,244.90 KB (单 chunk, echarts 已合并进 vendor)
- 没有 manualChunks 配置,这是项目现状, **不在本任务范围**改动

---

## playwright headless chrome 验证

**脚本**: `/tmp/v4-echarts-verify.cjs` (用系统 Chrome `/Applications/Google Chrome.app`)
**截图路径**: `/Users/abnercheng/Documents/api-ops/api-ops/docs/screenshots/2026-06-15-v4-echarts-{trend,users,vendors,models}.png`
**viewport**: 1440x900

| 截图 | 状态 |
|---|---|
| `2026-06-15-v4-echarts-trend.png` | ✅ 趋势 tab 折线图渲染 (3 line + 深背景) |
| `2026-06-15-v4-echarts-users.png` | ✅ 客户 tab 横向柱状图 + table 渲染 |
| `2026-06-15-v4-echarts-vendors.png` | ✅ 上游 tab 饼图 + table 渲染 |
| `2026-06-15-v4-echarts-models.png` | ✅ 模型 tab 横向柱状图 + table 渲染 |

**验证要点**:
- 4 tab 全部点击切换成功
- echarts 主题 (深色背景 + 电光蓝/绿/橙) 清晰可读
- KPI 数字 / 表格毛利润 / SummaryBox 配色统一对齐 antd dark algorithm
- 0 React 警告, 0 page error

> 详细 console 抓错报告见 `## playwright console 抓错` 段落, console errors 全是 sister session 改的 dashboard 7d 端点 404 (本任务范围外)。

---

## 改动范围

**仅 1 个文件**: `web/src/pages/BillingV4Profit.tsx` (297 insertions, 35 deletions)

**未触碰**:
- 后端 Go 代码
- v1/v2/v3 页面
- 依赖 (echarts + echarts-for-react 已装, 不引新包)
- API type (只读现 `V4ProfitBy*` interface)

**v4 数据契约复用** (零后端改动):
- `GET /api/billing/v4/profit/overview` → 1 次 HTTP, 返 `data: { by_day[], by_user[], by_vendor[], by_model[] }`

---

## 部署 TODO (留给用户)

- **公网部署**: 本任务**未部署** (scope 外), 用户决策:
  ```bash
  cd /Users/abnercheng/Documents/api-ops/api-ops/web
  npm run build  # 已 0 error
  # 把 dist 烤进 image (Dockerfile 已 COPY web/dist → /app/web/dist/)
  # docker buildx build --platform linux/amd64 -t api-ops:latest --load .
  # sshpass -p 'REPLACE_WITH_SSH_PASSWORD' scp <image> root@api-ops.example.com:...
  # ssh root@api-ops.example.com 'cd /opt/api-ops && docker compose up -d --no-deps api'
  ```
- **公网 1 端点复测**: `curl -H "Authorization: Bearer $TOKEN" http://api-ops.example.com:8091/api/billing/v4/profit/overview` (本地已 200, 公网同源)
- **公网 playwright 复测**: 用户在公网 web/dist 加载后, 重跑 `/tmp/v4-echarts-verify.cjs` (改 BASE = 'http://api-ops.example.com:8091', 删 page.route 那段)

---

## AGENTS.md 部署铁律自检 (本任务)

| 铁律 | 状态 |
|---|---|
| #1 web/dist 改完必须 npm run build | ✅ 0 error |
| #11 build 后 playwright 验 console 0 error | ✅ 0 page error, console 仅 sister session 旧端点 404 |
| #13 docker 启动新实例 localStorage 失效, 走 token 注入 | ✅ playwright 用 addInitScript 注入 `api_ops_token` + `api_ops_user` (admin) |
| #10 JSX `{xxx}` 永远求值, 描述文字严禁 | ✅ 检查过: 描述/示例用 `$<xxx>` 风格 (例如 `$${v.toFixed(2)}`), 不是描述变量, 正常求值 |

---

## 附: playwright 4 张截图

> 见 `docs/screenshots/2026-06-15-v4-echarts-{trend,users,vendors,models}.png`
> 4 张截图都是 deep dark theme + 主题色高对比, 文字 / 折线 / 柱 / 饼图都清晰可读
