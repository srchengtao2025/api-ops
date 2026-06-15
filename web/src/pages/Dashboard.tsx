import { useEffect, useState } from 'react'
import { Row, Col, Button } from 'antd'
import { DollarOutlined, ThunderboltOutlined, ApiOutlined, ReloadOutlined, LineChartOutlined } from '@ant-design/icons'
import { api } from '../api'

type Today = {
  date: string
  revenue_usd: number
  rpm: number
  tpm: number
}
type TrendItem = { date: string; revenue_usd: number }
type Trend7d = {
  items: TrendItem[]
  generated_at: number
  source_cached: boolean
}

export default function Dashboard() {
  // 总览模块 (2026-06-15 加回 7d trend):
  //   - /api/dashboard/today 走 admin /api/log/stat 1 次, 5s tick 拉
  //   - /api/dashboard/trend-7d 后端 cache 5min, 前端 5min 拉一次
  //   - admin 限流 18次/5min: 后端 5min 1 轮 7 调用 = 7 占用, 余 11 额度
  const [today, setToday] = useState<Today | null>(null)
  const [trend, setTrend] = useState<Trend7d | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string>('')
  const [now, setNow] = useState<string>('')

  const fetchToday = async () => {
    try {
      const t = await api.dashboardToday()
      setToday(t || null)
    } catch (e: any) {
      setError(e?.response?.data?.error?.message || e?.message || 'fetch failed')
    }
  }

  const fetchTrend = async () => {
    try {
      const t = await api.dashboardTrend7d()
      setTrend(t || null)
    } catch (e: any) {
      console.warn('trend-7d failed:', e?.response?.data?.error?.message || e?.message)
    }
  }

  const fetchAll = async () => {
    setLoading(true)
    setError('')
    await Promise.all([fetchToday(), fetchTrend()])
    setLoading(false)
  }

  useEffect(() => {
    fetchAll()
    const todayTimer = setInterval(fetchToday, 5000)
    const trendTimer = setInterval(fetchTrend, 5 * 60 * 1000)
    const clockTimer = setInterval(() => {
      const d = new Date()
      const pad = (n: number) => n.toString().padStart(2, '0')
      setNow(`${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`)
    }, 1000)
    return () => {
      clearInterval(todayTimer); clearInterval(trendTimer); clearInterval(clockTimer)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <div>
      <div className="page-title">
        <div>
          <h1>Dashboard <span className="tag-info">P0</span></h1>
          <div className="subtitle">今日 · {today?.date || '加载中...'} · 数据延迟 &lt; 15 min · {now}</div>
        </div>
        <div className="actions">
          <Button icon={<ReloadOutlined />} onClick={fetchAll} loading={loading}>刷新</Button>
        </div>
      </div>

      {error && (
        <div className="error-banner">
          <span>错误：{error}</span>
          <span className="hint">（admin /api/log/stat 调用失败。18次/5min 限流时返 429。详见 X-Data-Source 响应头）</span>
        </div>
      )}

      <Row gutter={[16, 16]} style={{ marginBottom: 20 }}>
        <Col span={8}>
          <div className="kpi-card kpi-success">
            <div className="kpi-label"><span className="status-dot success" />今日收入 (USD)</div>
            <div className="kpi-value">$<span>{(today?.revenue_usd || 0).toFixed(2)}</span></div>
            <div className="kpi-trend">来自 admin /api/log/stat (今日 type=2 总消耗)</div>
            <DollarOutlined style={{ position: 'absolute', right: 16, top: 16, fontSize: 20, color: 'var(--status-success)', opacity: 0.4 }} />
          </div>
        </Col>
        <Col span={8}>
          <div className="kpi-card kpi-info">
            <div className="kpi-label"><span className="status-dot info" />RPM (60s 滑窗)</div>
            <div className="kpi-value">{today?.rpm || 0}</div>
            <div className="kpi-trend">60s 内 type=2 请求数</div>
            <ThunderboltOutlined style={{ position: 'absolute', right: 16, top: 16, fontSize: 20, color: 'var(--accent-secondary)', opacity: 0.4 }} />
          </div>
        </Col>
        <Col span={8}>
          <div className="kpi-card kpi-warning">
            <div className="kpi-label"><span className="status-dot warning" />TPM (60s 滑窗)</div>
            <div className="kpi-value">{today?.tpm || 0}</div>
            <div className="kpi-trend">60s 内 type=2 tokens (prompt + completion)</div>
            <ApiOutlined style={{ position: 'absolute', right: 16, top: 16, fontSize: 20, color: 'var(--status-warning)', opacity: 0.4 }} />
          </div>
        </Col>
      </Row>

      <div className="ops-card">
        <div className="card-title">
          <span><LineChartOutlined style={{ color: 'var(--accent-primary)', marginRight: 6 }} />全站消耗趋势 · 最近 7 天 <span style={{ fontSize: 11, color: 'var(--text-tertiary)', fontWeight: 400 }}>(不含今天)</span></span>
          <span style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 11, color: 'var(--text-tertiary)' }}>
            <span>{trend?.source_cached ? '后端 cache 命中' : '后端实时拉'}</span>
            <span style={{ fontVariantNumeric: 'tabular-nums' }}>{trend ? new Date(trend.generated_at * 1000).toLocaleTimeString('zh-CN', { hour12: false }) : '--:--:--'}</span>
          </span>
        </div>
        <TrendBarChart items={trend?.items || []} />
      </div>

      <div style={{ marginTop: 16, fontSize: 12, color: 'var(--text-tertiary)' }}>
        数据源：admin /api/log/stat (today 5s tick + trend-7d 后端 5min cache). 限流 18次/5min.
      </div>
    </div>
  )
}

function TrendBarChart({ items }: { items: TrendItem[] }) {
  if (!items.length) {
    return <div style={{ height: 200, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--text-tertiary)', fontSize: 12 }}>暂无数据</div>
  }
  const max = Math.max(...items.map((i) => i.revenue_usd), 1)
  const total = items.reduce((s, i) => s + i.revenue_usd, 0)
  const avg = total / items.length
  const W = 800, H = 220
  const padding = { top: 20, right: 20, bottom: 36, left: 56 }
  const chartW = W - padding.left - padding.right
  const chartH = H - padding.top - padding.bottom
  const barW = (chartW / items.length) * 0.6
  const barGap = (chartW / items.length) * 0.4

  return (
    <div>
      <div style={{ display: 'flex', gap: 24, marginBottom: 12, fontSize: 12, color: 'var(--text-secondary)' }}>
        <span>合计 <b style={{ color: 'var(--text-primary)' }}>${total.toFixed(2)}</b></span>
        <span>日均 <b style={{ color: 'var(--text-primary)' }}>${avg.toFixed(2)}</b></span>
        <span>峰日 <b style={{ color: 'var(--accent-primary)' }}>${max.toFixed(2)}</b></span>
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} style={{ width: '100%', height: H, display: 'block' }} preserveAspectRatio="xMidYMid meet">
        {[0, 0.25, 0.5, 0.75, 1].map((p) => {
          const y = padding.top + chartH * (1 - p)
          return (
            <g key={p}>
              <line x1={padding.left} y1={y} x2={W - padding.right} y2={y} stroke="var(--border-subtle)" strokeWidth={1} strokeDasharray="3 3" />
              <text x={padding.left - 8} y={y + 4} textAnchor="end" fontSize={10} fill="var(--text-tertiary)">${(max * p).toFixed(0)}</text>
            </g>
          )
        })}
        {items.map((it, idx) => {
          const x = padding.left + idx * (barW + barGap) + barGap / 2
          const h = (it.revenue_usd / max) * chartH
          const y = padding.top + chartH - h
          const isPeak = it.revenue_usd === max
          return (
            <g key={it.date}>
              <rect x={x} y={y} width={barW} height={h} rx={3} fill={isPeak ? 'var(--accent-primary)' : 'var(--accent-secondary)'} opacity={isPeak ? 1 : 0.7} />
              <text x={x + barW / 2} y={y - 6} textAnchor="middle" fontSize={10} fill="var(--text-primary)" style={{ fontWeight: isPeak ? 600 : 400 }}>${it.revenue_usd.toFixed(0)}</text>
              <text x={x + barW / 2} y={H - padding.bottom + 18} textAnchor="middle" fontSize={10} fill="var(--text-secondary)">{it.date.slice(5)}</text>
            </g>
          )
        })}
      </svg>
    </div>
  )
}
