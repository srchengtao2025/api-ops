import { useEffect, useState } from 'react'
import { Tag, Button, Space, message, Tooltip } from 'antd'
import { ReloadOutlined, ApiOutlined, ThunderboltOutlined, AlertOutlined, CheckCircleOutlined, RiseOutlined, ShopOutlined, ClockCircleOutlined } from '@ant-design/icons'
import { api } from '../api'

type Channel = {
  id: number
  name: string
  type: number
  status: number
  models: string
  group: string
  used_quota: number
  balance: number
  response_time: number
  balance_updated_at: number
  vendor_code: string
  vendor_name: string
  health_24h: {
    request_count: number
    error_count: number
    error_rate: number
    prompt_tokens: number
    completion_tokens: number
    p50_latency_ms: number
    p95_latency_ms: number
    p99_latency_ms: number
    first_bucket_ts: number
    last_bucket_ts: number
  }
}

export default function ChannelHealth() {
  // 渠道健康度 (用户决策 2026-06-15 09:18):
  //   1) 24h 内有请求 + 启用 → 才显示
  //   2) 卡片 grid 展示, 关键信息 错误率 / 供应商 / 命中? / 余额
  //   3) 数据 = 24h SUM(request/error) 实时算 error_rate, 来源 channel_health_5min
  //   4) join channel_vendor_map + upstream_vendors 拿 vendor_name
  //   5) 5s tick 拉, 主动刷新
  const [channels, setChannels] = useState<Channel[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const fetchChannels = async () => {
    try {
      const r: any = await api.monitorChannels()
      setChannels(r?.items || [])
    } catch (e: any) {
      setError(e?.response?.data?.error?.message || e?.message || 'fetch failed')
    }
  }

  useEffect(() => {
    fetchChannels()
    const t = setInterval(fetchChannels, 10000)
    return () => clearInterval(t)
  }, [])

  // 5 KPI
  const kpis = (() => {
    const total = channels.length
    const totalReq = channels.reduce((s, c) => s + (c.health_24h?.request_count || 0), 0)
    const totalErr = channels.reduce((s, c) => s + (c.health_24h?.error_count || 0), 0)
    const errRate = totalReq > 0 ? (totalErr / totalReq) * 100 : 0
    const healthy = channels.filter((c) => (c.health_24h?.error_rate || 0) < 0.05).length
    const unhealthy = total - healthy
    return { total, totalReq, totalErr, errRate, healthy, unhealthy }
  })()

  return (
    <div>
      <div className="page-title">
        <div>
          <h1>渠道健康度看板 <span className="tag-info">P1</span></h1>
          <div className="subtitle">近 24h 活跃渠道 · 仅显示启用状态 · 数据每 5s 刷新</div>
        </div>
        <div className="actions">
          <Button icon={<ReloadOutlined />} onClick={fetchChannels} loading={loading}>刷新</Button>
        </div>
      </div>

      {error && (
        <div className="error-banner">
          <span>错误：{error}</span>
        </div>
      )}

      {/* 5 KPI */}
      <div className="kpi-grid" style={{ gridTemplateColumns: 'repeat(5, 1fr)', marginBottom: 20 }}>
        <div className="kpi-card kpi-info">
          <div className="kpi-label"><span className="status-dot info" />24h 活跃渠道</div>
          <div className="kpi-value">{kpis.total}</div>
          <div className="kpi-trend">启用 + 24h 有请求</div>
          <ApiOutlined style={{ position: 'absolute', right: 16, top: 16, fontSize: 20, color: 'var(--accent-secondary)', opacity: 0.4 }} />
        </div>
        <div className="kpi-card kpi-success">
          <div className="kpi-label"><span className="status-dot success" />健康 (错误率 &lt; 5%)</div>
          <div className="kpi-value">{kpis.healthy}</div>
          <div className="kpi-trend">24h 聚合</div>
          <CheckCircleOutlined style={{ position: 'absolute', right: 16, top: 16, fontSize: 20, color: 'var(--status-success)', opacity: 0.4 }} />
        </div>
        <div className="kpi-card kpi-warning">
          <div className="kpi-label"><span className="status-dot warning" />异常 (错误率 ≥ 5%)</div>
          <div className="kpi-value">{kpis.unhealthy}</div>
          <div className="kpi-trend">需关注</div>
          <AlertOutlined style={{ position: 'absolute', right: 16, top: 16, fontSize: 20, color: 'var(--status-warning)', opacity: 0.4 }} />
        </div>
        <div className="kpi-card">
          <div className="kpi-label"><span className="status-dot info" />24h 总请求数</div>
          <div className="kpi-value">{kpis.totalReq.toLocaleString()}</div>
          <div className="kpi-trend">type=2 成功 + 失败</div>
          <ThunderboltOutlined style={{ position: 'absolute', right: 16, top: 16, fontSize: 20, color: 'var(--accent-primary)', opacity: 0.4 }} />
        </div>
        <div className="kpi-card kpi-danger-stat">
          <div className="kpi-label"><span className="status-dot danger" />24h 错误率</div>
          <div className="kpi-value">{kpis.errRate.toFixed(2)}<span className="unit">%</span></div>
          <div className="kpi-trend">{kpis.totalErr} / {kpis.totalReq}</div>
          <RiseOutlined style={{ position: 'absolute', right: 16, top: 16, fontSize: 20, color: 'var(--status-danger)', opacity: 0.4 }} />
        </div>
      </div>

      {/* 渠道卡片 grid */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(360px, 1fr))', gap: 16 }}>
        {channels.map((c) => <ChannelCard key={c.id} ch={c} />)}
      </div>

      {channels.length === 0 && !loading && (
        <div style={{ padding: 40, textAlign: 'center', color: 'var(--text-tertiary)' }}>
          近 24h 无活跃渠道
        </div>
      )}
    </div>
  )
}

// ===== 单渠道卡片 =====
function ChannelCard({ ch }: { ch: Channel }) {
  const er = (ch.health_24h?.error_rate || 0) * 100
  const erColor = er < 1 ? 'success' : er < 5 ? 'info' : er < 20 ? 'warning' : 'danger'
  const p95 = ch.health_24h?.p95_latency_ms || 0
  const p95Color = p95 > 5000 ? 'danger' : p95 > 2000 ? 'warning' : 'success'

  return (
    <div className={`ops-card kpi-card kpi-${erColor}`} style={{ padding: 0 }}>
      {/* 顶部: 渠道名 + 状态点 */}
      <div style={{ padding: '14px 18px', borderBottom: '1px solid var(--border-subtle)' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <Tooltip title={ch.name}>
            <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 220 }}>
              #{ch.id} · {ch.name}
            </div>
          </Tooltip>
          <Tag color="success" style={{ marginLeft: 8 }}>enabled</Tag>
        </div>
        {ch.group && (
          <div style={{ marginTop: 6, fontSize: 11, color: 'var(--text-tertiary)' }}>
            {ch.group.split(',').slice(0, 3).map((g) => <Tag key={g} style={{ marginRight: 2, fontSize: 10 }}>{g}</Tag>)}
          </div>
        )}
      </div>

      {/* 中部: 3 个关键信息 大字 */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 0, padding: '14px 0' }}>
        <CardBigStat
          label="错误率"
          value={er.toFixed(2) + '%'}
          unit={er < 1 ? '优秀' : er < 5 ? '正常' : er < 20 ? '需关注' : '严重'}
          color={erColor}
        />
        <CardBigStat
          label="24h 请求"
          value={(ch.health_24h?.request_count || 0).toLocaleString()}
          unit={`${ch.health_24h?.error_count || 0} 错误`}
          color="primary"
          separator
        />
        <CardBigStat
          label="P95 延迟"
          value={p95 > 0 ? p95 + 'ms' : '--'}
          unit={p95 > 5000 ? '慢' : p95 > 2000 ? '一般' : '快'}
          color={p95Color}
        />
      </div>

      {/* 底部: 供应商 + 余额 + 模型数 */}
      <div style={{ padding: '10px 18px', borderTop: '1px solid var(--border-subtle)', display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: 11, color: 'var(--text-tertiary)' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
          <ShopOutlined />
          {ch.vendor_name || <span style={{ color: 'var(--text-tertiary)', fontStyle: 'italic' }}>未分配</span>}
          {ch.vendor_code && <Tag style={{ marginLeft: 2, fontSize: 9 }}>{ch.vendor_code}</Tag>}
        </div>
        <div>
          {ch.models ? `${ch.models.split(',').length} 模型` : '--'}
        </div>
      </div>
    </div>
  )
}

function CardBigStat({ label, value, unit, color, separator }: { label: string; value: string; unit: string; color: 'success' | 'info' | 'warning' | 'danger' | 'primary'; separator?: boolean }) {
  const colorMap = {
    success: 'var(--status-success)',
    info: 'var(--accent-secondary)',
    warning: 'var(--status-warning)',
    danger: 'var(--status-danger)',
    primary: 'var(--accent-primary)',
  }
  return (
    <div style={{
      padding: '0 12px',
      textAlign: 'center',
      borderRight: separator ? '1px solid var(--border-subtle)' : 'none',
    }}>
      <div style={{ fontSize: 10, color: 'var(--text-tertiary)', marginBottom: 4, textTransform: 'uppercase', letterSpacing: 0.5 }}>{label}</div>
      <div style={{ fontSize: 20, fontWeight: 600, color: colorMap[color], fontVariantNumeric: 'tabular-nums' }}>{value}</div>
      <div style={{ fontSize: 10, color: 'var(--text-tertiary)', marginTop: 2 }}>{unit}</div>
    </div>
  )
}
