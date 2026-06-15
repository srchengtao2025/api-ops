// BILLING v4 利润分析页 (PR #3 / 6, 2026-06-14)
//
// 需求:
//   1. 1 端点 (overview) 返汇总 + 趋势 + 3 维度拆分
//   2. 1 页面 3 tab: 汇总 / 客户 / 上游
//   3. 复用 v2 + v3 已有数据 (revenue + cost 反推)
//
// v4 echarts 升级 (2026-06-15):
//   - 趋势 tab: 手写 SVG bar → echarts 折线图 (3 line + smooth + dataZoom + tooltips)
//   - 客户/上游/模型 tab: 加 echarts 图, 跟 Table 上下排
//     · 客户: 横向柱状图 (top 10 客户 profit)
//     · 上游: 饼图 (5 vendor cost 占比)
//     · 模型: 横向柱状图 (top 10 model revenue)
//   - 主题: 跟 antd dark algorithm 对齐, 深空黑 #0B0E14 + 电光蓝/电光绿/电光橙
import { useEffect, useState, useMemo } from 'react'
import { Card, Space, Table, Tabs, Tag, Button, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import ReactECharts from 'echarts-for-react'
import { ReloadOutlined, DollarOutlined, RiseOutlined, ShopOutlined, UserOutlined, FundOutlined } from '@ant-design/icons'
import { api, V4ProfitByUser, V4ProfitByVendor, V4ProfitByModel, V4ProfitByDay } from '../api'
import { getUser } from '../api'

// echarts 主题: 跟 antd dark algorithm 对齐 (深空黑 + 电光蓝/电光绿/电光橙)
const ECHARTS_COLORS = {
  revenue: '#3B82F6',  // 电光蓝 (跟主色一致)
  cost: '#F59E0B',     // 电光橙
  profit: '#10B981',   // 电光绿
  text: '#E5E7EB',
  textMuted: '#9CA3AF',
  border: '#2A3346',
  bgTooltip: 'rgba(15, 23, 41, 0.95)',
}

const ECHARTS_BASE_OPTION = {
  textStyle: { color: ECHARTS_COLORS.text, fontFamily: 'Inter, "Source Han Sans SC", "PingFang SC", sans-serif' },
  tooltip: {
    trigger: 'axis',
    backgroundColor: ECHARTS_COLORS.bgTooltip,
    borderColor: ECHARTS_COLORS.border,
    borderWidth: 1,
    textStyle: { color: ECHARTS_COLORS.text, fontSize: 12 },
    axisPointer: { type: 'cross', crossStyle: { color: ECHARTS_COLORS.border } },
  },
  legend: {
    textStyle: { color: ECHARTS_COLORS.textMuted, fontSize: 12 },
    top: 0,
  },
  grid: { left: 60, right: 30, bottom: 60, top: 40, containLabel: true },
  xAxis: {
    type: 'category',
    axisLine: { lineStyle: { color: ECHARTS_COLORS.border } },
    axisLabel: { color: ECHARTS_COLORS.textMuted, fontSize: 11 },
    splitLine: { show: false },
  },
  yAxis: {
    type: 'value',
    axisLine: { lineStyle: { color: ECHARTS_COLORS.border } },
    axisLabel: {
      color: ECHARTS_COLORS.textMuted,
      fontSize: 11,
      formatter: (v: number) => `$${v.toLocaleString()}`,
    },
    splitLine: { lineStyle: { color: ECHARTS_COLORS.border, type: 'dashed', opacity: 0.3 } },
  },
}

export default function BillingV4Profit() {
  const [loading, setLoading] = useState(false)
  const [data, setData] = useState<{
    period_start: number
    period_end: number
    user_count: number
    total_revenue: number
    total_cost: number
    total_profit: number
    profit_rate: number
    by_day: V4ProfitByDay[]
    by_user: V4ProfitByUser[]
    by_vendor: V4ProfitByVendor[]
    by_model: V4ProfitByModel[]
  } | null>(null)
  const me = getUser()
  const canView = me?.role === 'admin' || me?.role === 'finance' || me?.role === 'viewer'

  const fetch = async () => {
    setLoading(true)
    try {
      const res: any = await api.v4ProfitOverview()
      setData(res)
    } catch (e: any) {
      message.error('加载失败: ' + (e?.response?.data?.error?.message || e?.message))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { if (canView) fetch() }, [])

  if (!canView) {
    return <div style={{ padding: 40, textAlign: 'center' }}>无权限</div>
  }

  return (
    <div>
      <Card
        title={
          <Space>
            <FundOutlined style={{ color: '#3B82F6' }} />
            <span>利润分析 (本月 至今)</span>
            <Tag color="blue">v4</Tag>
          </Space>
        }
        extra={
          <Button icon={<ReloadOutlined />} onClick={fetch} loading={loading}>
            刷新
          </Button>
        }
        style={{ marginBottom: 16 }}
      >
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 16 }}>
          <SummaryBox
            label="客户消耗 (USD)"
            value={data ? `$${data.total_revenue.toLocaleString(undefined, { minimumFractionDigits: 2 })}` : '-'}
            color="#3B82F6"
            icon={<DollarOutlined />}
          />
          <SummaryBox
            label="累计成本 (USD)"
            value={data ? `$${data.total_cost.toLocaleString(undefined, { minimumFractionDigits: 2 })}` : '-'}
            color="#F59E0B"
            icon={<ShopOutlined />}
          />
          <SummaryBox
            label="毛利 (USD)"
            value={data ? `$${data.total_profit.toLocaleString(undefined, { minimumFractionDigits: 2 })}` : '-'}
            color={data && data.total_profit > 0 ? '#10B981' : '#EF4444'}
            icon={<RiseOutlined />}
          />
          <SummaryBox
            label="毛利率"
            value={data ? `${(data.profit_rate * 100).toFixed(1)}%` : '-'}
            color={data && data.profit_rate >= 1 ? '#10B981' : data && data.profit_rate >= 0 ? '#F59E0B' : '#EF4444'}
            sub={data ? `客户数 ${data.user_count}` : ''}
          />
        </div>
      </Card>

      <Tabs
        defaultActiveKey="summary"
        items={[
          {
            key: 'summary',
            label: <span><RiseOutlined /> 趋势 (30 天)</span>,
            children: <SummaryTab data={data?.by_day || []} />,
          },
          {
            key: 'users',
            label: <span><UserOutlined /> 客户 (v2 视角, {data?.by_user.length || 0})</span>,
            children: <UsersTab users={data?.by_user || []} />,
          },
          {
            key: 'vendors',
            label: <span><ShopOutlined /> 上游 (v3 视角, {data?.by_vendor.length || 0})</span>,
            children: <VendorsTab vendors={data?.by_vendor || []} />,
          },
          {
            key: 'models',
            label: <span><DollarOutlined /> 模型 (top 10, {data?.by_model.length || 0})</span>,
            children: <ModelsTab models={data?.by_model || []} />,
          },
        ]}
      />

      <div style={{ color: '#9CA3AF', fontSize: 12, marginTop: 16, textAlign: 'center' }}>
        数据源: RoDB(newapi.logs) + v3 CalcLogCost 成本反推 ·
        公式: revenue = quota / 500000, cost = (revenue / group_ratio) × channel_discount ·
        利润率 = (revenue - cost) / cost
      </div>
    </div>
  )
}

function SummaryTab({ data }: { data: V4ProfitByDay[] }) {
  const option = useMemo(() => {
    if (data.length === 0) return null
    return {
      ...ECHARTS_BASE_OPTION,
      tooltip: {
        ...ECHARTS_BASE_OPTION.tooltip,
        valueFormatter: (v: number) => `$${v.toFixed(2)}`,
      },
      xAxis: { ...ECHARTS_BASE_OPTION.xAxis, data: data.map(d => d.date) },
      yAxis: { ...ECHARTS_BASE_OPTION.yAxis, name: 'USD', nameTextStyle: { color: ECHARTS_COLORS.textMuted, fontSize: 11 } },
      legend: { ...ECHARTS_BASE_OPTION.legend, data: ['客户消耗 (revenue)', '累计成本 (cost)', '毛利 (profit)'] },
      dataZoom: [
        { type: 'inside', start: 0, end: 100 },
        { type: 'slider', start: 0, end: 100, height: 20, bottom: 10, borderColor: ECHARTS_COLORS.border, fillerColor: 'rgba(59, 130, 246, 0.2)', handleStyle: { color: ECHARTS_COLORS.revenue }, textStyle: { color: ECHARTS_COLORS.textMuted } },
      ],
      series: [
        {
          name: '客户消耗 (revenue)',
          type: 'line',
          smooth: true,
          symbol: 'circle',
          symbolSize: 6,
          data: data.map(d => +d.revenue.toFixed(2)),
          itemStyle: { color: ECHARTS_COLORS.revenue },
          lineStyle: { width: 2 },
          areaStyle: { color: 'rgba(59, 130, 246, 0.12)' },
        },
        {
          name: '累计成本 (cost)',
          type: 'line',
          smooth: true,
          symbol: 'circle',
          symbolSize: 6,
          data: data.map(d => +d.cost.toFixed(2)),
          itemStyle: { color: ECHARTS_COLORS.cost },
          lineStyle: { width: 2 },
          areaStyle: { color: 'rgba(245, 158, 11, 0.10)' },
        },
        {
          name: '毛利 (profit)',
          type: 'line',
          smooth: true,
          symbol: 'circle',
          symbolSize: 6,
          data: data.map(d => +d.profit.toFixed(2)),
          itemStyle: { color: ECHARTS_COLORS.profit },
          lineStyle: { width: 2 },
          areaStyle: { color: 'rgba(16, 185, 129, 0.10)' },
        },
      ],
    }
  }, [data])

  if (data.length === 0) return <EmptyTab text="暂无趋势数据" />
  return (
    <div>
      <h4 style={{ marginTop: 0, color: '#E5E7EB' }}>30 天每日 USD (折线图: revenue / cost / profit, 可缩放)</h4>
      <div style={{ background: '#0F1729', border: '1px solid #2A3346', borderRadius: 6, padding: 8 }}>
        <ReactECharts
          option={option!}
          style={{ height: 360, width: '100%' }}
          notMerge={true}
          lazyUpdate={true}
          theme="dark"
        />
      </div>
    </div>
  )
}

function UsersTab({ users }: { users: V4ProfitByUser[] }) {
  const top10 = useMemo(() => {
    return [...users].sort((a, b) => b.profit - a.profit).slice(0, 10)
  }, [users])

  const chartOption = useMemo(() => {
    if (top10.length === 0) return null
    return {
      ...ECHARTS_BASE_OPTION,
      tooltip: { ...ECHARTS_BASE_OPTION.tooltip, trigger: 'axis', axisPointer: { type: 'shadow' } },
      legend: { show: false },
      grid: { left: 100, right: 30, bottom: 30, top: 20, containLabel: true },
      xAxis: {
        ...ECHARTS_BASE_OPTION.xAxis,
        type: 'value',
        axisLabel: { color: ECHARTS_COLORS.textMuted, fontSize: 11, formatter: (v: number) => `$${v.toLocaleString()}` },
      },
      yAxis: {
        ...ECHARTS_BASE_OPTION.yAxis,
        type: 'category',
        data: top10.slice().reverse().map(u => u.username),
        axisLabel: { color: ECHARTS_COLORS.textMuted, fontSize: 11 },
      },
      series: [
        {
          name: '毛利 (USD)',
          type: 'bar',
          data: top10.slice().reverse().map(u => +u.profit.toFixed(2)),
          itemStyle: {
            color: (params: any) => params.value >= 0 ? ECHARTS_COLORS.profit : ECHARTS_COLORS.cost,
            borderRadius: [0, 4, 4, 0],
          },
          label: {
            show: true,
            position: 'right',
            color: ECHARTS_COLORS.text,
            fontSize: 11,
            formatter: (params: any) => `$${params.value.toFixed(2)}`,
          },
        },
      ],
    }
  }, [top10])

  const columns: ColumnsType<V4ProfitByUser> = [
    { title: '客户', key: 'name', width: 180, render: (_, r) => <Space><UserOutlined />{r.username} <Tag>{r.user_id}</Tag></Space> },
    { title: '调用次数', dataIndex: 'request_count', width: 110, align: 'right', render: (v: number) => v.toLocaleString() },
    { title: '输入 tokens', dataIndex: 'prompt_tokens', width: 130, align: 'right', render: (v: number) => v.toLocaleString() },
    { title: '输出 tokens', dataIndex: 'completion_tokens', width: 130, align: 'right', render: (v: number) => v.toLocaleString() },
    { title: '缓存 tokens', dataIndex: 'cache_tokens', width: 130, align: 'right', render: (v: number) => v.toLocaleString() },
    { title: '消耗 (USD)', dataIndex: 'revenue', width: 130, align: 'right', render: (v: number) => `$${v.toLocaleString(undefined, { minimumFractionDigits: 2 })}` },
    { title: '成本 (USD)', dataIndex: 'cost', width: 130, align: 'right', render: (v: number) => `$${v.toLocaleString(undefined, { minimumFractionDigits: 2 })}` },
    { title: '毛利 (USD)', dataIndex: 'profit', width: 130, align: 'right', render: (v: number) => <b style={{ color: v > 0 ? '#10B981' : '#EF4444' }}>${v.toLocaleString(undefined, { minimumFractionDigits: 2 })}</b> },
    { title: '毛利率', dataIndex: 'profit_rate', width: 100, align: 'right', render: (v: number) => <Tag color={v >= 1 ? 'green' : 'orange'}>{(v * 100).toFixed(1)}%</Tag> },
  ]
  if (users.length === 0) return <EmptyTab text="暂无客户数据" />
  return (
    <div>
      <h4 style={{ marginTop: 0, color: '#E5E7EB' }}>Top 10 客户毛利 (USD)</h4>
      <div style={{ background: '#0F1729', border: '1px solid #2A3346', borderRadius: 6, padding: 8, marginBottom: 16 }}>
        <ReactECharts
          option={chartOption!}
          style={{ height: Math.max(280, top10.length * 32 + 60), width: '100%' }}
          notMerge={true}
          lazyUpdate={true}
          theme="dark"
        />
      </div>
      <Table<V4ProfitByUser> rowKey="user_id" columns={columns} dataSource={users} pagination={{ pageSize: 10, showSizeChanger: false }} size="middle" />
    </div>
  )
}

function VendorsTab({ vendors }: { vendors: V4ProfitByVendor[] }) {
  const top5 = useMemo(() => {
    return [...vendors].sort((a, b) => b.cost - a.cost).slice(0, 5)
  }, [vendors])

  const chartOption = useMemo(() => {
    if (top5.length === 0) return null
    return {
      ...ECHARTS_BASE_OPTION,
      tooltip: {
        trigger: 'item',
        backgroundColor: ECHARTS_COLORS.bgTooltip,
        borderColor: ECHARTS_COLORS.border,
        textStyle: { color: ECHARTS_COLORS.text, fontSize: 12 },
        formatter: (params: any) => `<b>${params.name}</b><br/>成本: $${params.value.toFixed(2)}<br/>占比: ${params.percent.toFixed(1)}%`,
      },
      legend: {
        bottom: 0,
        textStyle: { color: ECHARTS_COLORS.textMuted, fontSize: 12 },
      },
      series: [
        {
          name: '上游成本占比',
          type: 'pie',
          radius: ['45%', '72%'],
          center: ['50%', '45%'],
          avoidLabelOverlap: true,
          itemStyle: { borderRadius: 4, borderColor: '#0F1729', borderWidth: 2 },
          label: {
            color: ECHARTS_COLORS.text,
            fontSize: 12,
            formatter: '{b}\n${c}',
          },
          labelLine: { lineStyle: { color: ECHARTS_COLORS.border } },
          data: top5.map(v => ({ name: v.vendor_name, value: +v.cost.toFixed(2) })),
        },
      ],
    }
  }, [top5])

  const columns: ColumnsType<V4ProfitByVendor> = [
    { title: '上游', key: 'name', width: 200, render: (_, r) => <Space><ShopOutlined />{r.vendor_name} <Tag>{r.vendor_code}</Tag></Space> },
    { title: '调用次数', dataIndex: 'request_count', width: 110, align: 'right', render: (v: number) => v.toLocaleString() },
    { title: '消耗 (USD)', dataIndex: 'revenue', width: 140, align: 'right', render: (v: number) => `$${v.toLocaleString(undefined, { minimumFractionDigits: 2 })}` },
    { title: '成本 (USD)', dataIndex: 'cost', width: 140, align: 'right', render: (v: number) => `$${v.toLocaleString(undefined, { minimumFractionDigits: 2 })}` },
    { title: '毛利 (USD)', dataIndex: 'profit', width: 140, align: 'right', render: (v: number) => <b style={{ color: v > 0 ? '#10B981' : '#EF4444' }}>${v.toLocaleString(undefined, { minimumFractionDigits: 2 })}</b> },
    { title: '毛利率', dataIndex: 'profit_rate', width: 100, align: 'right', render: (v: number) => <Tag color={v >= 1 ? 'green' : 'orange'}>{(v * 100).toFixed(1)}%</Tag> },
  ]
  if (vendors.length === 0) return <EmptyTab text="暂无上游数据" />
  return (
    <div>
      <h4 style={{ marginTop: 0, color: '#E5E7EB' }}>Top 5 上游成本占比 (USD)</h4>
      <div style={{ background: '#0F1729', border: '1px solid #2A3346', borderRadius: 6, padding: 8, marginBottom: 16 }}>
        <ReactECharts
          option={chartOption!}
          style={{ height: 360, width: '100%' }}
          notMerge={true}
          lazyUpdate={true}
          theme="dark"
        />
      </div>
      <Table<V4ProfitByVendor> rowKey="vendor_code" columns={columns} dataSource={vendors} pagination={{ pageSize: 10, showSizeChanger: false }} size="middle" />
    </div>
  )
}

function ModelsTab({ models }: { models: V4ProfitByModel[] }) {
  const top10 = useMemo(() => {
    return [...models].sort((a, b) => b.revenue - a.revenue).slice(0, 10)
  }, [models])

  const chartOption = useMemo(() => {
    if (top10.length === 0) return null
    return {
      ...ECHARTS_BASE_OPTION,
      tooltip: { ...ECHARTS_BASE_OPTION.tooltip, trigger: 'axis', axisPointer: { type: 'shadow' } },
      legend: { show: false },
      grid: { left: 180, right: 30, bottom: 30, top: 20, containLabel: true },
      xAxis: {
        ...ECHARTS_BASE_OPTION.xAxis,
        type: 'value',
        axisLabel: { color: ECHARTS_COLORS.textMuted, fontSize: 11, formatter: (v: number) => `$${v.toLocaleString()}` },
      },
      yAxis: {
        ...ECHARTS_BASE_OPTION.yAxis,
        type: 'category',
        data: top10.slice().reverse().map(m => m.model_name),
        axisLabel: { color: ECHARTS_COLORS.textMuted, fontSize: 11 },
      },
      series: [
        {
          name: '客户消耗 (USD)',
          type: 'bar',
          data: top10.slice().reverse().map(m => +m.revenue.toFixed(2)),
          itemStyle: { color: ECHARTS_COLORS.revenue, borderRadius: [0, 4, 4, 0] },
          label: {
            show: true,
            position: 'right',
            color: ECHARTS_COLORS.text,
            fontSize: 11,
            formatter: (params: any) => `$${params.value.toFixed(2)}`,
          },
        },
      ],
    }
  }, [top10])

  const columns: ColumnsType<V4ProfitByModel> = [
    { title: '模型', dataIndex: 'model_name', width: 200 },
    { title: '调用次数', dataIndex: 'request_count', width: 110, align: 'right', render: (v: number) => v.toLocaleString() },
    { title: '消耗 (USD)', dataIndex: 'revenue', width: 140, align: 'right', render: (v: number) => `$${v.toLocaleString(undefined, { minimumFractionDigits: 2 })}` },
    { title: '成本 (USD)', dataIndex: 'cost', width: 140, align: 'right', render: (v: number) => `$${v.toLocaleString(undefined, { minimumFractionDigits: 2 })}` },
    { title: '毛利 (USD)', dataIndex: 'profit', width: 140, align: 'right', render: (v: number) => <b style={{ color: v > 0 ? '#10B981' : '#EF4444' }}>${v.toLocaleString(undefined, { minimumFractionDigits: 2 })}</b> },
    { title: '毛利率', dataIndex: 'profit_rate', width: 100, align: 'right', render: (v: number) => <Tag color={v >= 1 ? 'green' : 'orange'}>{(v * 100).toFixed(1)}%</Tag> },
  ]
  if (models.length === 0) return <EmptyTab text="暂无模型数据" />
  return (
    <div>
      <h4 style={{ marginTop: 0, color: '#E5E7EB' }}>Top 10 模型收入 (USD)</h4>
      <div style={{ background: '#0F1729', border: '1px solid #2A3346', borderRadius: 6, padding: 8, marginBottom: 16 }}>
        <ReactECharts
          option={chartOption!}
          style={{ height: Math.max(280, top10.length * 32 + 60), width: '100%' }}
          notMerge={true}
          lazyUpdate={true}
          theme="dark"
        />
      </div>
      <Table<V4ProfitByModel> rowKey="model_name" columns={columns} dataSource={models} pagination={{ pageSize: 10, showSizeChanger: false }} size="middle" />
    </div>
  )
}

function SummaryBox({ label, value, color, sub, icon }: { label: string; value: string; color?: string; sub?: string; icon?: any }) {
  return (
    <div style={{ padding: 12, background: '#0F1729', borderRadius: 6, border: '1px solid #2A3346' }}>
      <div style={{ fontSize: 12, color: '#9CA3AF' }}>{icon}{label}</div>
      <div style={{ fontSize: 22, fontWeight: 600, color: color || '#E5E7EB', marginTop: 4 }}>{value}</div>
      {sub && <div style={{ fontSize: 11, color: '#6B7280', marginTop: 2 }}>{sub}</div>}
    </div>
  )
}

function EmptyTab({ text }: { text: string }) {
  return <div style={{ padding: 60, textAlign: 'center', color: '#9CA3AF' }}>{text}</div>
}
