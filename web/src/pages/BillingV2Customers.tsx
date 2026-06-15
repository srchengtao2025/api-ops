// BILLING v2 客户对账单默认页 (PR #5 / 8, 2026-06-14)
//
// 需求 (用户原话):
//   1. 默认采用表格展示所有用户的消耗情况
//   2. 整个页面默认是当前自然月窗口下的累计消耗量
//   3. 字段: 输入/输出/缓存创建/缓存命中 tokens + 合计消耗金额
//   4. 每用户支持点击 "生成上个自然月账单" 按钮 → 异步任务
//
// 替代: 现有 /billing/customer (v1, 6 端点) 暂保留 6 个月 (RFC §7)
import { useEffect, useState } from 'react'
import { Button, Card, Modal, Space, Table, Tag, Tooltip, Checkbox, message } from 'antd'
import { ReloadOutlined, DownloadOutlined, ClockCircleOutlined, CheckCircleOutlined, CloseCircleOutlined, FileZipOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { api, V2CustomerMonthItem, V2ExportTask } from '../api'
import { getUser } from '../api'

export default function BillingV2Customers() {
  const [loading, setLoading] = useState(false)
  const [items, setItems] = useState<V2CustomerMonthItem[]>([])
  const [total, setTotal] = useState({ user_count: 0, total_revenue: 0, total_tokens: 0 })
  const [genOpen, setGenOpen] = useState(false)
  const [genUser, setGenUser] = useState<V2CustomerMonthItem | null>(null)
  const [formats, setFormats] = useState<string[]>(['html', 'xlsx'])
  const [recentTask, setRecentTask] = useState<V2ExportTask | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const me = getUser()
  const canExport = me?.role === 'admin' || me?.role === 'finance'
  const navigate = useNavigate()

  const fetch = async () => {
    setLoading(true)
    try {
      const res: any = await api.v2CurrentMonthOverview()
      setItems(res?.items || [])
      setTotal({
        user_count: res?.user_count || 0,
        total_revenue: res?.total_revenue || 0,
        total_tokens: res?.total_tokens || 0,
      })
    } catch (e: any) {
      message.error('加载失败: ' + (e?.response?.data?.error?.message || e?.message))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { fetch() }, [])

  const onOpenGen = (row: V2CustomerMonthItem) => {
    setGenUser(row)
    setFormats(['html', 'xlsx'])
    setGenOpen(true)
  }

  const onSubmitGen = async () => {
    if (!genUser) return
    if (formats.length === 0) {
      message.warning('至少选一个格式')
      return
    }
    setSubmitting(true)
    try {
      const res: any = await api.v2ExportLastMonth(genUser.user_id, { formats: formats.join(',') })
      message.success(`任务已创建, 转到任务中心`)
      setGenOpen(false)
      setRecentTask(res)
      // 3s 后跳到任务中心
      setTimeout(() => navigate('/billing/exports'), 500)
    } catch (e: any) {
      const msg = e?.response?.data?.error?.message || e?.message
      message.error('创建失败: ' + msg)
    } finally {
      setSubmitting(false)
    }
  }

  // 直接点行内 "导出" 按钮: 跳过 Modal, 默认 html,xlsx, 跳任务中心
  const onQuickExport = async (row: V2CustomerMonthItem) => {
    try {
      await api.v2ExportLastMonth(row.user_id, { formats: 'html,xlsx' })
      message.success(`任务已创建: ${row.username} 上月账单, 转到任务中心`)
      setTimeout(() => navigate('/billing/exports'), 500)
    } catch (e: any) {
      const msg = e?.response?.data?.error?.message || e?.message
      message.error('创建失败: ' + msg)
    }
  }

  return (
    <div>
      <h2>客户对账 (v2) - 当月累计</h2>
      <p style={{ color: '#888' }}>
        默认展示当前自然月 ({new Date().getFullYear()}-{String(new Date().getMonth() + 1).padStart(2, '0')}) 所有用户的消耗情况。
        点击 "生成上月账单" 按钮异步导出 Excel + HTML 格式, 完成后可在"任务中心"下载。
      </p>

      <Space size="large" style={{ marginBottom: 16 }}>
        <Card size="small" style={{ minWidth: 140 }}>
          <div style={{ fontSize: 12, color: '#999' }}>用户数</div>
          <div style={{ fontSize: 24, fontWeight: 600 }}>{total.user_count}</div>
        </Card>
        <Card size="small" style={{ minWidth: 200 }}>
          <div style={{ fontSize: 12, color: '#999' }}>本月总收入 (USD)</div>
          <div style={{ fontSize: 24, fontWeight: 600, color: '#cf1322' }}>
            ${total.total_revenue.toFixed(2)}
          </div>
        </Card>
        <Card size="small" style={{ minWidth: 200 }}>
          <div style={{ fontSize: 12, color: '#999' }}>本月总 tokens</div>
          <div style={{ fontSize: 24, fontWeight: 600 }}>
            {total.total_tokens.toLocaleString()}
          </div>
        </Card>
        <Button icon={<ReloadOutlined />} onClick={fetch} loading={loading}>
          刷新
        </Button>
        <Button icon={<FileZipOutlined />} onClick={() => navigate('/billing/exports')}>
          任务中心
        </Button>
      </Space>

      <Table
        size="small"
        loading={loading}
        dataSource={items}
        rowKey="user_id"
        pagination={{ pageSize: 20, showSizeChanger: true }}
        scroll={{ x: 1100 }}
        columns={[
          { title: '用户ID', dataIndex: 'user_id', width: 80, fixed: 'left', sorter: (a, b) => a.user_id - b.user_id },
          { title: '用户名', dataIndex: 'username', width: 160, fixed: 'left' },
          { title: '调用次数', dataIndex: 'request_count', width: 100, sorter: (a, b) => a.request_count - b.request_count, render: (v) => v.toLocaleString() },
          {
            title: '输入 tokens', dataIndex: 'prompt_tokens', width: 130,
            sorter: (a, b) => a.prompt_tokens - b.prompt_tokens,
            render: (v) => v.toLocaleString(),
          },
          {
            title: '输出 tokens', dataIndex: 'completion_tokens', width: 130,
            sorter: (a, b) => a.completion_tokens - b.completion_tokens,
            render: (v) => v.toLocaleString(),
          },
          {
            title: '缓存创建', dataIndex: 'cache_tokens', width: 110,
            sorter: (a, b) => a.cache_tokens - b.cache_tokens,
            render: (v) => v > 0 ? <span style={{ color: '#52c41a' }}>{v.toLocaleString()}</span> : '-',
          },
          {
            title: '缓存命中', dataIndex: 'cache_tokens', width: 110,
            sorter: (a, b) => a.cache_tokens - b.cache_tokens,
            render: (v) => v > 0 ? <span style={{ color: '#52c41a' }}>{v.toLocaleString()}</span> : '-',
          },
          {
            title: '合计金额 (USD)', dataIndex: 'revenue_usd', width: 150,
            sorter: (a, b) => a.revenue_usd - b.revenue_usd,
            render: (v) => (
              <span style={{ fontWeight: 600, color: '#cf1322' }}>
                ${v.toFixed(2)}
              </span>
            ),
          },
          {
            title: '操作', width: 160, fixed: 'right',
            render: (_, row) =>
              canExport ? (
                <Space size={4}>
                  <Tooltip title="生成上个自然月账单 (默认 HTML + XLSX 打包 ZIP)">
                    <Button size="small" type="primary" onClick={() => onOpenGen(row)}>
                      生成上月账单
                    </Button>
                  </Tooltip>
                </Space>
              ) : <Tag>无权限</Tag>,
          },
        ]}
      />

      {/* 生成账单 Modal */}
      <Modal
        title={genUser ? `生成 ${genUser.username} 上月账单` : '生成账单'}
        open={genOpen}
        onCancel={() => setGenOpen(false)}
        onOk={onSubmitGen}
        okText="创建任务"
        cancelText="取消"
        confirmLoading={submitting}
      >
        {genUser && (
          <div>
            <p>
              将为 <strong>{genUser.username}</strong> (ID: {genUser.user_id}) 生成上个月 (上个自然月) 的账单。
            </p>
            <div style={{ marginBottom: 8 }}>选择输出格式 (可多选):</div>
            <Checkbox.Group
              value={formats}
              onChange={(v) => setFormats(v as string[])}
              options={[
                { value: 'html', label: 'HTML (浏览器可读)' },
                { value: 'xlsx', label: 'Excel 多 sheet (财务处理)' },
              ]}
            />
            <div style={{ marginTop: 12, fontSize: 12, color: '#999' }}>
              <ClockCircleOutlined /> 异步执行, 完成后在"任务中心"下载 ZIP
              <br />
              限制: 每用户最多同时 2 个生成任务
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}
