# 错误率 ≥ 20% 红边发光 + 2s 脉动 · 报告 (PR #3 of monitor module)

**PR**: 监控中心 · 错误率 ≥ 20% 渠道卡片红色发光边框 + 2s 脉动动画
**日期**: 2026-06-15 09:26 (用户决策) / commit 时间 09:28
**Commit**: `4edbc85`
**状态**: ✅ **PASS** (CSS @keyframes + kpi-danger class 触发, 6 卡中 1 张红边)

---

## TL;DR

监控中心 PR #3: 错误率 ≥ 20% 的渠道卡片加红色 1px 边框 + 0.4 alpha 12px 外发光 + 0.08 alpha 内发光 + 2s ease-in-out 无限呼吸脉动. CSS +0.4KB (gzip +0.1KB).

| 检查项 | 状态 | 详情 |
|---|---|---|
| CSS @keyframes | ✅ | `kpi-danger-pulse` 2s ease-in-out infinite |
| 触发条件 | ✅ | `.kpi-card.kpi-danger` + 错误率 ≥ 20% |
| 体积 | ✅ | CSS +0.4KB / gzip +0.1KB |
| playwright | ✅ | 0 exception + 0 console error |
| 截图 | ✅ | `docs/screenshots/2026-06-15-monitor-danger-border.png` (152 KB) |

---

## 改动清单 (git show --stat)

```
docs/screenshots/2026-06-15-monitor-danger-border.png | Bin 0 -> 152367 bytes
web/src/styles.css                                 |  24 +++++++++++++++++++++
2 files changed, 24 insertions(+)
```

**纯 CSS PR**, 无后端改动, 无 SPA 改动 (kpi-danger class 已在 ChannelHealth.tsx 用).

---

## 特效实现 (web/src/styles.css)

### @keyframes kpi-danger-pulse

```css
@keyframes kpi-danger-pulse {
  0%, 100% {
    box-shadow:
      0 0 12px rgba(239, 68, 68, 0.4),    /* 外发光 12px 0.4 alpha */
      inset 0 0 8px rgba(239, 68, 68, 0.08); /* 内发光 8px 0.08 alpha */
  }
  50% {
    box-shadow:
      0 0 20px rgba(239, 68, 68, 0.7),    /* 外发光 20px 0.7 alpha 呼吸峰值 */
      inset 0 0 12px rgba(239, 68, 68, 0.15); /* 内发光 12px 0.15 alpha */
  }
}

.kpi-card.kpi-danger {
  border: 1px solid var(--status-danger);  /* #EF4444 红色 1px */
  animation: kpi-danger-pulse 2s ease-in-out infinite;
}
```

---

## 触发条件 (复用已有 ChannelCard 逻辑)

`ChannelHealth.tsx` 中按错误率选色:

```typescript
function erColor(rate: number): string {
  if (rate >= 0.20) return 'kpi-danger';  // 红边呼吸
  if (rate >= 0.10) return 'kpi-warning'; // 橙色
  if (rate >= 0.05) return 'kpi-info';    // 蓝色
  return 'kpi-success';                    // 绿色
}
```

错误率 ≥ 20% → `.kpi-card.kpi-danger` class → 触发红边呼吸.
错误率 < 20% → 不触发 (按其他色条等级).

---

## 公网验证 (token 007f4c03...)

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://api-ops.example.com:8091/api/monitor/channels" | jq '[.data.items[] | select(.error_rate >= 0.20)] | length'
# 1  ← 24h 数据中错误率 ≥ 20% 的渠道数
```

---

## playwright 验证

- 0 exception + 0 console error
- 截图: `docs/screenshots/2026-06-15-monitor-danger-border.png` (152 KB, 1 张红边呼吸卡)
- 视觉效果: 卡片 1px 红边 + 12-20px 外发光呼吸 (2s 周期)

---

## 体积

| 文件 | 增量 | gzip 后 |
|---|---|---|
| web/src/styles.css | +0.4KB | +0.1KB |
| web/dist CSS | 8.46KB → 8.86KB (+0.4KB) | 2.23KB → 2.33KB (+0.1KB) |
| web/dist JS | 不变 (1.18MB) | 不变 (377KB) |

---

## 关键经验

1. **CSS 动画优先于 JS 动画** — @keyframes 由浏览器 GPU 合成, 0 JS 性能开销
2. **box-shadow 实现发光** — 比 filter: drop-shadow 兼容性更好, 跟 border-radius 配合圆角
3. **ease-in-out 适合呼吸** — 比 linear 自然, 0%/100% 起点 50% 峰值, 来回呼吸
4. **复用已有 class** — 不改 ChannelHealth.tsx, 纯 CSS 改动, PR 风险最小

---

## 后续 PR (monitor module)

- PR #4 (`675dcc2`): 错误率新口径 (业务请求 + 独立错误) + 红边呼吸加强
