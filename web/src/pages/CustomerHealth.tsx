// 客户健康度模块 (2026-07-08)
//
// 48h 错误率 + 缓存复用率自动统计, 支持异步 HTML 详情导出
// 多站点: intl + cn 共享, 通过 SiteSwitcher 切换
//
// 复用 v2 异步导出风格: 5s 轮询 + 状态机 + 下载
import { useEffect, useRef, useState } from 'react'
import { Button, Card, Modal, Select, Space, Statistic, Table, Tabs, Tag, Tooltip, message, Progress } from 'antd'
import {
  ReloadOutlined,
  DownloadOutlined,
  ExportOutlined,
  CheckCircleOutlined,
  WarningOutlined,
  CloseCircleOutlined,
  LoadingOutlined,
  StopOutlined,
  ClockCircleOutlined,
} from '@ant-design/icons'
import { api, getUser, V2ExportTask, HealthItem, HealthLevel, HealthDetail } from '../api'

const LEVEL_COLOR: Record<HealthLevel, string> = {
  healthy: 'success',
  warning: 'warning',
  critical: 'error',
}
const LEVEL_ICON: Record<HealthLevel, JSX.Element> = {
  healthy: <CheckCircleOutlined />,
  warning: <WarningOutlined />,
  critical: <CloseCircleOutlined />,
}
const LEVEL_LABEL: Record<HealthLevel, string> = {
  healthy: '健康',
  warning: '关注',
  critical: '告警',
}

const STATUS_COLOR: Record<string, string> = {
  pending: 'default',
  running: 'processing',
  success: 'success',
  failed: 'error',
  cancelled: 'warning',
}
const STATUS_ICON: Record<string, JSX.Element> = {
  pending: <ClockCircleOutlined />,
  running: <LoadingOutlined spin />,
  success: <CheckCircleOutlined />,
  failed: <CloseCircleOutlined />,
  cancelled: <StopOutlined />,
}
const STATUS_LABEL: Record<string, string> = {
  pending: '排队中',
  running: '生成中',
  success: '完成',
  failed: '失败',
  cancelled: '已取消',
}

export default function CustomerHealth() {
  const me = getUser()
  const canExport = me?.role === 'admin' || me?.role === 'finance'
  const [period, setPeriod] = useState<string>('48h')
  const [level, setLevel] = useState<HealthLevel | undefined>(undefined)
  const [loading, setLoading] = useState(false)
  const [stats, setStats] = useState({ total: 0, healthy: 0, warning: 0, critical: 0 })
  const [items, setItems] = useState<HealthItem[]>([])

  // 详情 modal
  const [detailOpen, setDetailOpen] = useState(false)
  const [detail, setDetail] = useState<HealthDetail | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)

  // 任务列表
  const [tasks, setTasks] = useState<V2ExportTask[]>([])
  const [taskTotal, setTaskTotal] = useState(0)
  const [taskLoading, setTaskLoading] = useState(false)
  const [taskStatusFilter, setTaskStatusFilter] = useState<string | undefined>(undefined)
  const timerRef = useRef<number | null>(null)

  const fetchOverview = async () => {
    setLoading(true)
    try {
      const params: any = { period }
      if (level) params.level = level
      const res: any = await api.customerHealthOverview(params)
      setStats(res?.stats || { total: 0, healthy: 0, warning: 0, critical: 0 })
      setItems(res?.items || [])
    } catch (e: any) {
      message.error('加载健康度失败: ' + (e?.message || 'unknown'))
    } finally {
      setLoading(false)
    }
  }

  const fetchDetail = async (userId: number) => {
    setDetailLoading(true)
    setDetailOpen(true)
    try {
      const res: any = await api.customerHealthDetail(userId, { period })
      setDetail(res?.detail || null)
    } catch (e: any) {
      message.error('加载详情失败: ' + (e?.message || 'unknown'))
      setDetailOpen(false)
    } finally {
      setDetailLoading(false)
    }
  }

  const fetchTasks = async (silent = false) => {
    if (!silent) setTaskLoading(true)
    try {
      const params: any = { limit: 100 }
      if (taskStatusFilter) params.status = taskStatusFilter
      const res: any = await api.customerHealthExportTasks(params)
      setTasks(res?.items || [])
      setTaskTotal(res?.total || 0)
    } catch (e: any) {
      if (!silent) message.error('加载任务失败: ' + (e?.message || 'unknown'))
    } finally {
      if (!silent) setTaskLoading(false)
    }
  }

  useEffect(() => { fetchOverview() }, [period, level])
  useEffect(() => { fetchTasks() }, [taskStatusFilter])

  // 5s 轮询: 有 running/pending 才轮 (跟 v2 一致)
  useEffect(() => {
    const hasActive = tasks.some((t) => t.status === 'running' || t.status === 'pending')
    if (hasActive) {
      timerRef.current = window.setInterval(() => {
        fetchTasks(true)
      }, 5000)
    }
    return () => {
      if (timerRef.current) {
        clearInterval(timerRef.current)
        timerRef.current = null
      }
    }
  }, [tasks])

  const onExport = async (row: HealthItem, kind: 'errors' | 'hits') => {
    try {
      if (kind === 'errors') {
        await api.customerHealthExportErrors(row.user_id, { period })
      } else {
        await api.customerHealthExportHits(row.user_id, { period })
      }
      message.success(`已提交 ${LEVEL_LABEL[row.health_level] || ''} 客户的 ${kind === 'errors' ? '错误' : '命中'}详情导出, 请到任务中心查看`)
      // 切到任务 tab
      // (简单起见不切, 让用户手动切)
      fetchTasks()
    } catch (e: any) {
      message.error('提交失败: ' + (e?.message || 'unknown'))
    }
  }

  const onDownload = (task: V2ExportTask) => {
    if (task.status !== 'success') {
      message.warning('任务未完成, 状态=' + task.status)
      return
    }
    const a = document.createElement('a')
    a.href = api.customerHealthDownloadUrl(task.task_id)
    const kind = task.vendor_code || 'detail'
    a.download = `health_${task.username}_${kind}_${task.period}.html`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
  }

  const formatSize = (bytes?: number): string => {
    if (!bytes) return '-'
    if (bytes < 1024) return bytes + ' B'
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
    return (bytes / (1024 * 1024)).toFixed(2) + ' MB'
  }

  const formatTime = (ts?: string): string => {
    if (!ts) return '-'
    return new Date(ts).toLocaleString('zh-CN', { hour12: false })
  }

  const formatTimeUnix = (ts: number): string => {
    return new Date(ts * 1000).toLocaleString('zh-CN', { hour12: false })
  }

  return (
    <div>
      <h2>客户健康度</h2>
      <p style={{ color: '#888' }}>
        自动统计最近 {period} 客户的错误率 + 缓存复用率, 支持异步 HTML 详情导出. 站点: {"intl"}.
      </p>

      <Space size="large" style={{ marginBottom: 16 }} wrap>
        <Card size="small" style={{ minWidth: 160 }}>
          <Statistic title="总客户数" value={stats.total} />
        </Card>
        <Card size="small" style={{ minWidth: 160, borderLeft: '3px solid #52c41a' }}>
          <Statistic title="🟢 健康" value={stats.healthy} valueStyle={{ color: '#52c41a' }} />
        </Card>
        <Card size="small" style={{ minWidth: 160, borderLeft: '3px solid #faad14' }}>
          <Statistic title="🟡 关注" value={stats.warning} valueStyle={{ color: '#faad14' }} />
        </Card>
        <Card size="small" style={{ minWidth: 160, borderLeft: '3px solid #f5222d' }}>
          <Statistic title="🔴 告警" value={stats.critical} valueStyle={{ color: '#f5222d' }} />
        </Card>
        <Select
          value={period}
          onChange={setPeriod}
          style={{ width: 110 }}
          options={[
            { value: '48h', label: '48h' },
            { value: '7d', label: '7d' },
            { value: '30d', label: '30d' },
          ]}
        />
        <Select
          value={level}
          onChange={setLevel}
          placeholder="全部等级"
          allowClear
          style={{ width: 120 }}
          options={[
            { value: 'healthy', label: '🟢 健康' },
            { value: 'warning', label: '🟡 关注' },
            { value: 'critical', label: '🔴 告警' },
          ]}
        />
        <Button icon={<ReloadOutlined />} onClick={fetchOverview} loading={loading}>
          刷新
        </Button>
      </Space>

      <Tabs
        defaultActiveKey="overview"
        items={[
          {
            key: 'overview',
            label: '客户概览',
            children: (
              <Table
                size="small"
                loading={loading}
                dataSource={items}
                rowKey="user_id"
                pagination={{ pageSize: 50, showSizeChanger: true }}
                scroll={{ x: 1400 }}
                columns={[
                  {
                    title: '等级',
                    dataIndex: 'health_level',
                    width: 90,
                    fixed: 'left',
                    render: (lvl: HealthLevel) => (
                      <Tag icon={LEVEL_ICON[lvl]} color={LEVEL_COLOR[lvl]}>
                        {LEVEL_LABEL[lvl]}
                      </Tag>
                    ),
                    sorter: (a: HealthItem, b: HealthItem) => a.health_level.localeCompare(b.health_level),
                  },
                  { title: '客户', dataIndex: 'username', width: 140, fixed: 'left' },
                  { title: 'UID', dataIndex: 'user_id', width: 80 },
                  {
                    title: '错误率',
                    dataIndex: 'error_rate',
                    width: 90,
                    render: (v: number) => (v * 100).toFixed(2) + '%',
                    sorter: (a: HealthItem, b: HealthItem) => a.error_rate - b.error_rate,
                  },
                  {
                    title: '缓存复用率',
                    dataIndex: 'cache_rate',
                    width: 110,
                    render: (v: number) => (v * 100).toFixed(2) + '%',
                    sorter: (a: HealthItem, b: HealthItem) => a.cache_rate - b.cache_rate,
                  },
                  {
                    title: '请求数',
                    dataIndex: 'request_count',
                    width: 100,
                    render: (v: number) => v.toLocaleString(),
                    sorter: (a: HealthItem, b: HealthItem) => a.request_count - b.request_count,
                  },
                  {
                    title: '错误数',
                    dataIndex: 'error_count',
                    width: 90,
                    render: (v: number) => v.toLocaleString(),
                    sorter: (a: HealthItem, b: HealthItem) => a.error_count - b.error_count,
                  },
                  {
                    title: '总输入 (M)',
                    dataIndex: 'prompt_tokens',
                    width: 110,
                    render: (v: number) => (v / 1e6).toFixed(2),
                  },
                  {
                    title: '缓存命中 (M)',
                    dataIndex: 'cache_tokens',
                    width: 110,
                    render: (v: number) => (v / 1e6).toFixed(2),
                  },
                  {
                    title: '原因',
                    dataIndex: 'health_reasons',
                    width: 300,
                    ellipsis: true,
                    render: (s: string) => (
                      <Tooltip title={s}>
                        <span style={{ fontSize: 12, color: '#666' }}>{s}</span>
                      </Tooltip>
                    ),
                  },
                  {
                    title: '操作',
                    width: 200,
                    fixed: 'right',
                    render: (_, row: HealthItem) => (
                      <Space size={4}>
                        <Button size="small" onClick={() => fetchDetail(row.user_id)}>
                          详情
                        </Button>
                        {canExport && (
                          <>
                            <Tooltip title="导出错误详情 HTML (异步)">
                              <Button
                                size="small"
                                icon={<ExportOutlined />}
                                onClick={() => onExport(row, 'errors')}
                                disabled={row.error_count === 0}
                              >
                                错误
                              </Button>
                            </Tooltip>
                            <Tooltip title="导出命中详情 HTML (异步)">
                              <Button
                                size="small"
                                icon={<ExportOutlined />}
                                onClick={() => onExport(row, 'hits')}
                                disabled={row.cache_tokens === 0}
                              >
                                命中
                              </Button>
                            </Tooltip>
                          </>
                        )}
                      </Space>
                    ),
                  },
                ]}
              />
            ),
          },
          {
            key: 'tasks',
            label: `任务中心 (${taskTotal})`,
            children: (
              <div>
                <Space style={{ marginBottom: 12 }}>
                  <Select
                    value={taskStatusFilter}
                    onChange={setTaskStatusFilter}
                    placeholder="全部状态"
                    allowClear
                    style={{ width: 160 }}
                    options={[
                      { value: 'pending', label: '排队中' },
                      { value: 'running', label: '生成中' },
                      { value: 'success', label: '完成' },
                      { value: 'failed', label: '失败' },
                    ]}
                  />
                  <Button icon={<ReloadOutlined />} onClick={() => fetchTasks()}>刷新</Button>
                </Space>
                <Table
                  size="small"
                  loading={taskLoading}
                  dataSource={tasks}
                  rowKey="task_id"
                  pagination={{ pageSize: 20 }}
                  scroll={{ x: 1300 }}
                  columns={[
                    {
                      title: 'TaskID',
                      dataIndex: 'task_id',
                      width: 140,
                      render: (id: string) => (
                        <Tooltip title={id}>
                          <code style={{ fontSize: 11 }}>{id.slice(0, 12)}...</code>
                        </Tooltip>
                      ),
                    },
                    { title: '客户', dataIndex: 'username', width: 140 },
                    {
                      title: '类型',
                      dataIndex: 'vendor_code',
                      width: 80,
                      render: (v: string) => (
                        <Tag color={v === 'errors' ? 'red' : 'blue'}>{v || '-'}</Tag>
                      ),
                    },
                    { title: '周期', dataIndex: 'period', width: 80 },
                    {
                      title: '状态',
                      dataIndex: 'status',
                      width: 110,
                      render: (s: string) => (
                        <Tag icon={STATUS_ICON[s]} color={STATUS_COLOR[s]}>
                          {STATUS_LABEL[s] || s}
                        </Tag>
                      ),
                    },
                    {
                      title: '进度',
                      dataIndex: 'progress',
                      width: 130,
                      render: (p: number, row: V2ExportTask) => {
                        if (row.status === 'running') return <Progress percent={Math.max(p, 5)} size="small" status="active" />
                        if (row.status === 'success') return <Progress percent={100} size="small" status="success" />
                        if (row.status === 'failed') return <Progress percent={p} size="small" status="exception" />
                        return <Progress percent={p} size="small" />
                      },
                    },
                    {
                      title: '大小',
                      dataIndex: 'file_size',
                      width: 90,
                      render: (s: number) => formatSize(s),
                    },
                    { title: '操作人', dataIndex: 'operator', width: 100 },
                    {
                      title: '创建',
                      dataIndex: 'created_at',
                      width: 150,
                      render: (t: string) => formatTime(t),
                    },
                    {
                      title: '完成',
                      dataIndex: 'finished_at',
                      width: 150,
                      render: (t: string) => formatTime(t),
                    },
                    {
                      title: '操作',
                      width: 120,
                      fixed: 'right',
                      render: (_, row: V2ExportTask) => (
                        <Space size={4}>
                          {row.status === 'success' && (
                            <Button
                              size="small"
                              type="primary"
                              icon={<DownloadOutlined />}
                              onClick={() => onDownload(row)}
                            >
                              下载
                            </Button>
                          )}
                          {row.status === 'failed' && (
                            <Tooltip title={row.error_msg || '失败'}>
                              <Tag color="error">失败</Tag>
                            </Tooltip>
                          )}
                        </Space>
                      ),
                    },
                  ]}
                />
                <div style={{ marginTop: 8, fontSize: 12, color: '#999' }}>
                  有 running/pending 任务时 5s 自动轮询, 否则停止轮询. 文件 30 天自动清理.
                </div>
              </div>
            ),
          },
        ]}
      />

      {/* 详情 modal */}
      <Modal
        open={detailOpen}
        onCancel={() => setDetailOpen(false)}
        footer={null}
        width={1100}
        title={detail ? `${detail.username} (uid=${detail.user_id}) 健康度详情` : '客户健康度详情'}
        loading={detailLoading}
      >
        {detail && (
          <div>
            <Space size="large" style={{ marginBottom: 16 }} wrap>
              <Statistic title="总请求" value={detail.request_count} />
              <Statistic title="成功" value={detail.success_count} valueStyle={{ color: '#52c41a' }} />
              <Statistic title="错误" value={detail.error_count} valueStyle={{ color: '#f5222d' }} />
              <Statistic
                title="错误率"
                value={(detail.error_rate * 100).toFixed(2)}
                suffix="%"
                valueStyle={{ color: detail.error_rate > 0.1 ? '#f5222d' : detail.error_rate > 0.02 ? '#faad14' : '#52c41a' }}
              />
              <Statistic
                title="缓存复用率"
                value={(detail.cache_rate * 100).toFixed(2)}
                suffix="%"
                valueStyle={{ color: detail.cache_rate < 0.7 ? '#f5222d' : detail.cache_rate < 0.9 ? '#faad14' : '#52c41a' }}
              />
            </Space>
            <div style={{ marginBottom: 12, color: '#666', fontSize: 13 }}>
              {detail.health_reasons}
            </div>
            {detail.errors_truncated && (
              <div style={{ background: '#fffbe6', padding: 8, borderRadius: 4, marginBottom: 8, fontSize: 12 }}>
                ⚠️ 错误数据 &gt; 5000 条, 已截断, 完整数据请走"导出错误详情"
              </div>
            )}
            <Tabs
              defaultActiveKey="errors"
              items={[
                {
                  key: 'errors',
                  label: `错误 (${detail.errors.length})`,
                  children: (
                    <Table
                      size="small"
                      dataSource={detail.errors}
                      rowKey="id"
                      pagination={{ pageSize: 20 }}
                      scroll={{ x: 1000 }}
                      columns={[
                        { title: '时间', dataIndex: 'created_at', width: 150, render: (t: number) => formatTimeUnix(t) },
                        { title: '模型', dataIndex: 'model_name', width: 140 },
                        { title: '渠道', dataIndex: 'channel_id', width: 80 },
                        { title: '输入', dataIndex: 'prompt_tokens', width: 80 },
                        { title: '缓存', dataIndex: 'cache_tokens', width: 80 },
                        {
                          title: '错误',
                          dataIndex: 'content',
                          render: (c: string) => (
                            <Tooltip title={c}>
                              <span style={{ fontFamily: 'monospace', fontSize: 12, maxWidth: 500, display: 'inline-block', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                {c || '(无内容)'}
                              </span>
                            </Tooltip>
                          ),
                        },
                      ]}
                    />
                  ),
                },
                {
                  key: 'hits',
                  label: `命中 (${detail.hits.length})`,
                  children: (
                    <Table
                      size="small"
                      dataSource={detail.hits}
                      rowKey="id"
                      pagination={{ pageSize: 20 }}
                      scroll={{ x: 900 }}
                      columns={[
                        { title: '时间', dataIndex: 'created_at', width: 150, render: (t: number) => formatTimeUnix(t) },
                        { title: '模型', dataIndex: 'model_name', width: 140 },
                        { title: '渠道', dataIndex: 'channel_id', width: 80 },
                        { title: '输入', dataIndex: 'prompt_tokens', width: 100 },
                        { title: '缓存命中', dataIndex: 'cache_tokens', width: 100, render: (v: number) => <span style={{ color: '#52c41a', fontWeight: 600 }}>{v.toLocaleString()}</span> },
                        {
                          title: '命中率',
                          width: 90,
                          render: (_, r: any) => r.prompt_tokens > 0 ? ((r.cache_tokens / r.prompt_tokens) * 100).toFixed(2) + '%' : '-',
                        },
                      ]}
                    />
                  ),
                },
              ]}
            />
          </div>
        )}
      </Modal>
    </div>
  )
}
