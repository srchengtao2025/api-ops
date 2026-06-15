// BILLING v3 上游对账默认页 (PR #5 / 7, 2026-06-14)
//
// 需求 (用户原话 2026-06-14 21:00):
//   1. 根据供应商管理里面的录入, 把供应商在这里进行表格列出
//   2. 将该供应商底下归属的几个渠道在这个月产生的消耗
//      和消耗反推的累计成本以及计算出来的利润率进行展示
//   3. 每个供应商后面有按钮, 可以生成上个月的对账单 (异步 ZIP, 复用 v2 任务中心)
//
// 字段定义 (RFC §2):
//   - 消耗 (revenue)   = log.quota / 500000
//   - 累计成本 (cost)  = (revenue / group_ratio) × channel_discount
//   - 利润率 (margin)  = (revenue - cost) / cost  ("赚几倍")
//
// 双层结构: vendor (合) + channel (子)
import { useEffect, useState } from 'react'
import { Button, Card, Modal, Space, Table, Tag, Tooltip, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { ReloadOutlined, FileZipOutlined, DollarOutlined, ShopOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { api, V3UpstreamVendor, V3UpstreamChannel, V2ExportTask } from '../api'
import { getUser } from '../api'

// 表格行: vendor (合) + 嵌套 channel (子行)
type Row = {
  type: 'vendor' | 'channel'
  vendor_code: string
  vendor_name?: string
  channel_id?: number
  channel_name?: string
  discount?: number
  request_count: number
  total_cost: number
  total_revenue: number
  total_profit: number
  profit_rate: number
  children?: V3UpstreamChannel[]
}

export default function BillingV3Upstream() {
  const [loading, setLoading] = useState(false)
  const [items, setItems] = useState<V3UpstreamVendor[]>([])
  const [totals, setTotals] = useState({ vendor_count: 0, channel_count: 0, total_cost: 0, total_revenue: 0, total_profit: 0 })
  const [genOpen, setGenOpen] = useState(false)
  const [genVendor, setGenVendor] = useState<string>('') // 空 = 全部
  const [submitting, setSubmitting] = useState(false)
  const [recentTasks, setRecentTasks] = useState<V2ExportTask[]>([])
  const me = getUser()
  const canExport = me?.role === 'admin' || me?.role === 'finance'
  const navigate = useNavigate()

  const fetch = async () => {
    setLoading(true)
    try {
      const res: any = await api.v3UpstreamCurrentMonthOverview()
      setItems(res?.items || [])
      setTotals({
        vendor_count: res?.vendor_count || 0,
        channel_count: res?.channel_count || 0,
        total_cost: res?.total_cost || 0,
        total_revenue: res?.total_revenue || 0,
        total_profit: res?.total_profit || 0,
      })
    } catch (e: any) {
      message.error('加载失败: ' + (e?.response?.data?.error?.message || e?.message))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { fetch() }, [])

  // 提交生成上月对账
  const onSubmitGen = async () => {
    setSubmitting(true)
    try {
      const res: any = await api.v3UpstreamExportLastMonth({
        vendor_code: genVendor || undefined,
        formats: 'html,xlsx',
      })
      message.success(`已创建 ${res.vendor_count} 个上游对账任务 (${res.period})`)
      setGenOpen(false)
      // 跳任务中心
      setTimeout(() => navigate('/billing/exports'), 500)
    } catch (e: any) {
      message.error('创建失败: ' + (e?.response?.data?.error?.message || e?.message))
    } finally {
      setSubmitting(false)
    }
  }

  // 转双层 row: vendor + 嵌套 channel
  const rows: Row[] = items.flatMap((v): Row[] => {
    const vendorRow: Row = {
      type: 'vendor',
      vendor_code: v.vendor_code,
      vendor_name: v.vendor_name || v.vendor_code,
      request_count: v.request_count,
      total_cost: v.total_cost,
      total_revenue: v.total_revenue,
      total_profit: v.total_profit,
      profit_rate: v.profit_rate,
      children: v.channels,
    }
    const channelRows: Row[] = (v.channels || []).map((c): Row => ({
      type: 'channel',
      vendor_code: v.vendor_code,
      channel_id: c.channel_id,
      channel_name: c.channel_name,
      discount: c.discount,
      request_count: c.request_count,
      total_cost: c.total_cost,
      total_revenue: c.total_revenue,
      total_profit: c.total_profit,
      profit_rate: c.profit_rate,
    }))
    return [vendorRow, ...channelRows]
  })

  const columns: ColumnsType<Row> = [
    {
      title: '上游 / 渠道',
      key: 'name',
      width: 280,
      render: (_, r) => {
        if (r.type === 'vendor') {
          return (
            <Space>
              <ShopOutlined style={{ color: '#1677ff' }} />
              <b>{r.vendor_name}</b>
              <Tag color="blue">{r.vendor_code}</Tag>
            </Space>
          )
        }
        return (
          <Space style={{ paddingLeft: 24 }}>
            <span style={{ color: '#999' }}>└─</span>
            <span>{r.channel_name || `ch-${r.channel_id}`}</span>
            {r.discount !== undefined && (
              <Tooltip title="该渠道在上游对账成本反推里使用的折扣系数 (0.24 = 4.2 折)">
                <Tag color="orange">折扣 {r.discount.toFixed(2)}</Tag>
              </Tooltip>
            )}
          </Space>
        )
      },
    },
    {
      title: '调用次数',
      dataIndex: 'request_count',
      width: 110,
      align: 'right',
      render: (v: number) => v.toLocaleString(),
    },
    {
      title: '客户消耗 (USD)',
      dataIndex: 'total_revenue',
      width: 160,
      align: 'right',
      render: (v: number) => `$${v.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`,
    },
    {
      title: '累计成本 (USD)',
      dataIndex: 'total_cost',
      width: 160,
      align: 'right',
      render: (v: number) => `$${v.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`,
    },
    {
      title: '毛利 (USD)',
      dataIndex: 'total_profit',
      width: 140,
      align: 'right',
      render: (v: number) => `$${v.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`,
    },
    {
      title: '利润率',
      dataIndex: 'profit_rate',
      width: 110,
      align: 'right',
      render: (v: number) => {
        const pct = (v * 100).toFixed(1)
        const color = v >= 2 ? 'green' : v >= 1 ? 'lime' : v >= 0 ? 'orange' : 'red'
        return <Tag color={color}>{pct}%</Tag>
      },
    },
    ...(canExport
      ? [{
          title: '操作',
          key: 'action',
          width: 140,
          render: (_: any, r: Row) => {
            if (r.type !== 'vendor') return null
            return (
              <Button
                size="small"
                icon={<FileZipOutlined />}
                onClick={() => {
                  setGenVendor(r.vendor_code)
                  setGenOpen(true)
                }}
              >
                生成上月对账
              </Button>
            )
          },
        } as any]
      : []),
  ]

  return (
    <div>
      <Card
        title={
          <Space>
            <DollarOutlined style={{ color: '#1677ff' }} />
            <span>上游对账 (本月 至今)</span>
            <Tag color="blue">v3</Tag>
          </Space>
        }
        extra={
          <Space>
            <Button icon={<ReloadOutlined />} onClick={fetch} loading={loading}>
              刷新
            </Button>
            {canExport && (
              <Button
                type="primary"
                icon={<FileZipOutlined />}
                onClick={() => {
                  setGenVendor('')
                  setGenOpen(true)
                }}
              >
                生成全部上游上月对账
              </Button>
            )}
          </Space>
        }
        style={{ marginBottom: 16 }}
      >
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(5, 1fr)', gap: 16 }}>
          <SummaryBox label="上游数" value={`${totals.vendor_count}`} />
          <SummaryBox label="渠道数" value={`${totals.channel_count}`} />
          <SummaryBox label="客户消耗 (USD)" value={`$${totals.total_revenue.toLocaleString(undefined, { minimumFractionDigits: 2 })}`} color="#1677ff" />
          <SummaryBox label="累计成本 (USD)" value={`$${totals.total_cost.toLocaleString(undefined, { minimumFractionDigits: 2 })}`} color="#d48806" />
          <SummaryBox label="毛利率" value={totals.total_cost > 0 ? `${((totals.total_profit / totals.total_cost) * 100).toFixed(1)}%` : '-'} color={totals.total_profit > 0 ? 'green' : 'red'} />
        </div>
      </Card>

      <Table<Row>
        rowKey={(r) => `${r.type}-${r.vendor_code}-${r.channel_id || 0}`}
        columns={columns}
        dataSource={rows}
        loading={loading}
        pagination={false}
        size="middle"
        expandable={{
          defaultExpandAllRows: true,
          showExpandColumn: false,
        }}
        rowClassName={(r) => r.type === 'vendor' ? 'row-vendor' : 'row-channel'}
        footer={() => (
          <div style={{ color: '#999', fontSize: 12 }}>
            成本反推公式: cost = (revenue / group_ratio) × channel_discount · 利润率 = (revenue - cost) / cost ·
            数据源: RoDB(newapi.logs) + OPS.channel_vendor_map ·
            <a onClick={() => navigate('/billing/exports')}> 任务中心</a>
          </div>
        )}
      />

      {/* 生成任务确认弹窗 */}
      <Modal
        title={`生成 ${genVendor || '全部上游'} 上月对账`}
        open={genOpen}
        onCancel={() => setGenOpen(false)}
        onOk={onSubmitGen}
        confirmLoading={submitting}
        okText="确认创建任务"
        cancelText="取消"
      >
        <p>将异步生成上游对账 ZIP (HTML + XLSX), 复用 v2 任务中心.</p>
        <ul>
          <li>格式: HTML + XLSX</li>
          <li>范围: 上月 (即本月 1 号往前推一个月)</li>
          <li>输出: /data/billing-exports/&lt;taskID&gt;.zip (复用 v2 任务 ID 命名规则)</li>
          <li>权限: admin / finance</li>
          <li>限流: 每用户 ≤ 2 个 running 任务</li>
        </ul>
        {genVendor && <p>当前选择: <b>{genVendor}</b></p>}
        {!genVendor && <p>当前选择: <b>全部上游</b> ({items.length} 个)</p>}
      </Modal>
    </div>
  )
}

function SummaryBox({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div style={{ padding: 12, background: '#fafafa', borderRadius: 6, border: '1px solid #f0f0f0' }}>
      <div style={{ fontSize: 12, color: '#999' }}>{label}</div>
      <div style={{ fontSize: 20, fontWeight: 600, color: color || '#1f1f1f', marginTop: 4 }}>{value}</div>
    </div>
  )
}
