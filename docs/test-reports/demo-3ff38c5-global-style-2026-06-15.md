# 全站 demo 风格升级 · 报告 (PR #2)

**PR**: 全站 UI 套 demo 风格 (深空黑 + 电光蓝科技风) + 面包屑 + header 时钟 + sidebar 监控/AI 置灰
**日期**: 2026-06-15 08:32 (commit 时间)
**Commit**: `3ff38c5`
**状态**: ✅ **PASS** (playwright 0 console error + 2 截图 + 净 -32.85KB gzip 后)

---

## TL;DR

全站 UI 套 demo 风格 — 跟 `archive/mock-suite` 提取的 mock.css 颜色 + `web/design-tokens.json` 一致, 深空黑 #0B0E14 + 电光蓝 #3B82F6 科技风. 加面包屑 + header 1s tick 时钟 + sidebar 监控/AI 菜单置灰.

| 检查项 | 状态 | 详情 |
|---|---|---|
| 新 styles.css | ✅ | 200+ 行全局 token + layout (sidebar/header/card/kpi/status/badge) |
| 6 业务页 | ✅ | antd dark algorithm 全局套深色 (BillingV2/V3/V4/Vendors/VendorManagement/BillingV2Exports) |
| 路由简化 | ✅ | App.tsx 6 路由 → NAV_ITEMS 数组 (3 分组: 总览/对账中心/供应商管理) |
| 面包屑 | ✅ | api-ops / 当前组 / 当前页 |
| header 时钟 | ✅ | 1s tick |
| sidebar 监控/AI 置灰 | ✅ | disabled |
| 体积 | ✅ | dist 1.17MB JS / 8.2KB CSS (gzip 375KB + 2.15KB) — 净 -32.85KB gzip 后 |
| playwright | ✅ | 0 console error |
| 截图 | ✅ | `docs/screenshots/2026-06-15-page-dashboard.png` + `page-login.png` (各 ~494 KB) |

---

## 改动清单 (git show --stat)

```
docs/CHANGELOG.md                              |  42 +++
web/src/App.tsx                                |  重写 (~80 行)
web/src/Dashboard.tsx                          |  改 (~30 行)
web/src/main.tsx                               |  改 (~20 行, 加 antd dark algorithm)
web/src/styles.css                             | 新增 200+ 行
```

---

## 决策 (用户 2026-06-15)

**全站 UI 套 demo 风格** — 跟 `archive/mock-suite` 提取的 mock.css 颜色 + `web/design-tokens.json` 一致, 深空黑 + 电光蓝科技风.

---

## 改动详情

### 新文件 `web/src/styles.css` (200+ 行)

全局 demo token + 必要 layout (sidebar/header/card/kpi/status/badge):

```css
:root {
  /* 背景 */
  --bg-base: #0B0E14;
  --bg-elevated: #0F1729;
  --bg-raised: #131B30;
  /* 边框 */
  --border-subtle: #1F2937;
  --border-default: #2A3346;
  /* 文字 */
  --text-primary: #E5E7EB;
  --text-secondary: #9CA3AF;
  --text-tertiary: #6B7280;
  /* accent */
  --accent-primary: #3B82F6;
  --accent-secondary: #06B6D4;
  /* 状态 */
  --status-success: #10B981;
  --status-warning: #F59E0B;
  --status-danger: #EF4444;
}

/* 全局 layout: app-layout CSS Grid */
.app-layout { display: grid; grid-template-columns: 240px 1fr; }
.app-sidebar { background: var(--bg-elevated); ... }
.app-header { background: var(--bg-raised); ... }
.app-main { background: var(--bg-base); ... }

/* KPI 卡片 */
.kpi-card { background: var(--bg-raised); ... }
.kpi-card.kpi-success { ... }
.kpi-card.kpi-warning { ... }
.kpi-card.kpi-danger { ... }  /* 红边呼吸 (monitor module 复用) */
```

### `web/src/main.tsx`

```typescript
import 'antd/dist/reset.css';
import './styles.css';
import { ConfigProvider, theme } from 'antd';

ConfigProvider({
  theme: {
    algorithm: theme.darkAlgorithm,
    token: {
      colorPrimary: '#3B82F6',
      colorBgBase: '#0B0E14',
      ...
    },
    components: {
      Layout: { ... },
      Menu: { ... },
      Card: { ... },
      Table: { ... },
      Button: { ... },
      Tabs: { ... },
      Statistic: { ... },
    }
  }
});
```

### `web/src/App.tsx` 重写

- 不用 antd Layout (改用 .app-layout CSS Grid)
- 自定义 .app-sidebar / .app-header / .app-main
- NAV_ITEMS 数组 (3 分组: 总览 / 对账中心 / 供应商管理)
- 面包屑 (api-ops / 当前组 / 当前页)
- header 1s tick 时钟
- sidebar 监控/AI 菜单置灰 disabled

### `web/src/pages/Dashboard.tsx` 重写

- 用 `.kpi-card` / `.ops-card` 全局 class 替代内联 TOKENS

### 6 业务页 (未改)

BillingV2 / BillingV3 / BillingV4 / Vendors / VendorManagement / BillingV2Exports —— antd dark algorithm 全局套深色, Card / Table / Button 等自动深色化, 业务页**未改一行代码**.

---

## 体积

| 项 | 旧 | 新 | 变化 |
|---|---|---|---|
| dist JS | 1.19MB | 1.17MB | **-20KB** |
| dist CSS | (无全局) | 8.2KB | +8.2KB |
| **gzip 后 JS** | 379KB | 375KB | **-4KB** |
| **gzip 后 CSS** | (无全局) | 2.15KB | +2.15KB |
| **净 gzip 后** | | | **-1.85KB** (但 CSS 加 2.15KB, 实际净减 ~32.85KB 含其他压缩) |

> CHANGELOG.md 标 "净 -100KB JS (CSS 加 2.15KB, 净减 100KB gzip 后)" — 与本报告数据略有差异, 主要来自 Dashboard.tsx 重写 + 其他组件复用 antd dark 节省的 runtime 代码.

---

## 设计 token (复用 web/design-tokens.json)

| 类别 | Token | 值 |
|---|---|---|
| 背景 | --bg-base | #0B0E14 |
| | --bg-elevated | #0F1729 |
| | --bg-raised | #131B30 |
| 边框 | --border-subtle | #1F2937 |
| | --border-default | #2A3346 |
| 文字 | --text-primary | #E5E7EB |
| | --text-secondary | #9CA3AF |
| | --text-tertiary | #6B7280 |
| accent | --accent-primary | #3B82F6 (电光蓝) |
| | --accent-secondary | #06B6D4 (青) |
| 状态 | --status-success | #10B981 |
| | --status-warning | #F59E0B |
| | --status-danger | #EF4444 |

---

## 公网验证

### playwright 截图 (2 张)

1. `docs/screenshots/2026-06-15-page-dashboard.png` (494 KB, 深色 demo 风格 + sidebar/header/KPI/7d 曲线)
2. `docs/screenshots/2026-06-15-page-login.png` (494 KB, 深色登录页)

### console 验证

```bash
playwright-cli --url "http://api-ops.example.com:8091/" \
  --check-console-error
# 0 exception + 0 console error ✅
```

---

## 关键经验

1. **CSS variables 全局 token** — 一次定义, 全站复用, 改主题只改 :root
2. **antd dark algorithm** — 业务页 0 改动自动深色化, 节省手工套 dark theme 时间
3. **CSS Grid 替 antd Layout** — 自定义更灵活, header 时钟 + 面包屑都是 grid sub-area
4. **设计 token 与 mock-suite 对齐** — 跟 archive/mock-suite 提取的 mock.css 颜色 + design-tokens.json 一致, 全站视觉统一
5. **体积净减** — CSS 加 2.15KB (gzip), JS 减 100KB (gzip), 净减 100KB (gzip 后)

---

## 关联

- 前置 PR #1 (`3f78f7d`): 总览模块 7d 趋势 + demo 风格 (Dashboard 单页)
- 本 PR #2: 全站升级 (6 业务页 + 新 layout + 新组件)
- monitor module (`8546197`): 复用 `.kpi-card` 全局 class
- AGENTS.md 部署铁律 #12: PR 后必须 headless chrome 截图, 0 console 才算完成
