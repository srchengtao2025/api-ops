// BILLING v2 任务中心 (PR #6 / 8, 2026-06-14 RFC §6.2)
//
// 需求 (用户原话):
//   4. 异步任务, 单独任务中心, 完成后下载 zip
//
// 设计:
//   - 表格列: TaskID / 用户 / 周期 / 状态 / 进度 / 文件大小 / 创建时间 / 操作
//   - 5s setInterval 轮询 (running/pending 状态才轮, 减少请求)
//   - 下载按钮: 调 window.open(v2ExportDownloadUrl(taskId)) 或 a[download]
//   - 取消按钮: 调 v2CancelTask, 仅 pending 状态可点
//   - 失败显示 error_msg 红色
//   - 30 天后自动清理 (后端 PR #7 调度器)
import { useEffect, useState, useRef } from 'react'
import { Button, Card, Select, Space, Table, Tag, Tooltip, Progress, message, Modal, DatePicker, Form, InputNumber } from 'antd'
import dayjs, { Dayjs } from 'dayjs'
import { ReloadOutlined, DownloadOutlined, ClockCircleOutlined, CheckCircleOutlined, CloseCircleOutlined, LoadingOutlined, StopOutlined } from '@ant-design/icons'
import { api, V2ExportTask } from '../api'
import { getUser } from '../api'

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

export default function BillingV2Exports() {
  const [list, setList] = useState<V2ExportTask[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [statusFilter, setStatusFilter] = useState<string | undefined>(undefined)
  // 2026-07-15: 按指定日期区间导出 UI state
  const [periodExportUid, setPeriodExportUid] = useState<number | undefined>(undefined)
  const [periodExportRange, setPeriodExportRange] = useState<[Dayjs | null, Dayjs | null]>([null, null])
  const [periodExporting, setPeriodExporting] = useState(false)
  const me = getUser()
  const canCancel = me?.role === 'admin' || me?.role === 'finance'
  const timerRef = useRef<number | null>(null)

  const fetch = async (silent = false) => {
    if (!silent) setLoading(true)
    try {
      const params: any = { limit: 100 }
      if (statusFilter) params.status = statusFilter
      const res: any = await api.v2ExportTasks(params)
      setList(res?.items || [])
      setTotal(res?.total || 0)
    } catch (e: any) {
      const msg = e?.response?.data?.error?.message || e?.message
      if (!silent) message.error('加载失败: ' + msg)
    } finally {
      if (!silent) setLoading(false)
    }
  }

  useEffect(() => { fetch() }, [statusFilter])

  // 5s 轮询: 有 running/pending 状态才轮 (优化请求)
  useEffect(() => {
    const hasActive = list.some((t) => t.status === 'running' || t.status === 'pending')
    if (hasActive) {
      timerRef.current = window.setInterval(() => {
        fetch(true) // silent = true, 不闪 loading
      }, 5000)
    }
    return () => {
      if (timerRef.current) {
        clearInterval(timerRef.current)
        timerRef.current = null
      }
    }
  }, [list])

  const onDownload = (task: V2ExportTask) => {
    if (task.status !== 'success') {
      message.warning('任务未完成, 状态=' + task.status)
      return
    }
    // 用 a[download] 触发浏览器下载 (调 GET /download, 走 JWT 拦截器)
    const a = document.createElement('a')
    a.href = api.v2ExportDownloadUrl(task.task_id)
    a.download = `${task.username}_${task.period}_statement.zip`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
  }

  // 2026-07-15: 按指定日期区间导出对账单
  const onExportPeriod = async () => {
    if (!periodExportUid || !periodExportRange[0] || !periodExportRange[1]) {
      message.warning('请填写用户 ID 和日期区间')
      return
    }
    const start = periodExportRange[0]
    const end = periodExportRange[1]
    if (!end.isAfter(start)) {
      message.warning('结束日期必须晚于起始日期')
      return
    }
    if (end.diff(start, 'day') > 365) {
      message.warning('日期范围不能超过 365 天')
      return
    }
    setPeriodExporting(true)
    try {
      const res: any = await api.v2ExportPeriod(periodExportUid, {
        start: start.format('YYYY-MM-DD'),
        end: end.format('YYYY-MM-DD'),
        formats: 'html,xlsx',
      })
      message.success(`已入队: ${res.task_id} (${res.start_date} ~ ${res.end_date})`)
      // 5s 后刷新列表
      setTimeout(() => fetch(true), 5000)
    } catch (e: any) {
      const msg = e?.response?.data?.error?.message || e?.message
      message.error('提交失败: ' + msg)
    } finally {
      setPeriodExporting(false)
    }
  }

  const onCancel = (task: V2ExportTask) => {
    Modal.confirm({
      title: `取消任务?`,
      content: `${task.username} ${task.period} (TaskID: ${task.task_id.slice(0, 8)}...)`,
      okText: '取消任务',
      okButtonProps: { danger: true },
      cancelText: '不取消',
      onOk: async () => {
        try {
          await api.v2CancelTask(task.task_id)
          message.success('已取消')
          fetch()
        } catch (e: any) {
          const msg = e?.response?.data?.error?.message || e?.message
          message.error('取消失败: ' + msg)
        }
      },
    })
  }

  // 格式化文件大小
  const formatSize = (bytes: number): string => {
    if (bytes === 0) return '-'
    if (bytes < 1024) return bytes + ' B'
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
    return (bytes / (1024 * 1024)).toFixed(2) + ' MB'
  }

  // 格式化时间
  const formatTime = (ts?: string): string => {
    if (!ts) return '-'
    const d = new Date(ts)
    return d.toLocaleString('zh-CN', { hour12: false })
  }

  return (
    <div>
      <h2>账单导出 - 任务中心</h2>
      <p style={{ color: '#888' }}>
        异步账单导出任务, 默认 5s 轮询. 完成后状态变为"完成"可下载 ZIP (含 HTML + XLSX).
        30 天后自动清理.
      </p>

      {/* 2026-07-15: 按指定日期区间导出 (任意 [start, end) 区间, 不限月份) */}
      <Card size="small" style={{ marginBottom: 16, background: '#fafafa' }}>
        <Space wrap size="middle" align="end">
          <Form.Item label="用户 ID" style={{ marginBottom: 0 }}>
            <InputNumber
              placeholder="uid"
              min={1}
              value={periodExportUid}
              onChange={(v) => setPeriodExportUid(v ?? undefined)}
              style={{ width: 120 }}
            />
          </Form.Item>
          <Form.Item label="起始日期 (含)" style={{ marginBottom: 0 }}>
            <DatePicker
              value={periodExportRange[0]}
              onChange={(d) => setPeriodExportRange([d, periodExportRange[1]])}
              placeholder="开始日期"
              format="YYYY-MM-DD"
              disabledDate={(d) => d && d > dayjs().endOf('day')}
            />
          </Form.Item>
          <Form.Item label="结束日期 (不含)" style={{ marginBottom: 0 }}>
            <DatePicker
              value={periodExportRange[1]}
              onChange={(d) => setPeriodExportRange([periodExportRange[0], d])}
              placeholder="结束日期 (不含)"
              format="YYYY-MM-DD"
              disabledDate={(d) => d && d > dayjs().endOf('day')}
            />
          </Form.Item>
          <Button
            type="primary"
            loading={periodExporting}
            onClick={onExportPeriod}
            disabled={!periodExportUid || !periodExportRange[0] || !periodExportRange[1]}
          >
            按日期导出对账单
          </Button>
        </Space>
      </Card>

      <Space size="large" style={{ marginBottom: 16 }}>
        <Card size="small" style={{ minWidth: 140 }}>
          <div style={{ fontSize: 12, color: '#999' }}>总任务数</div>
          <div style={{ fontSize: 24, fontWeight: 600 }}>{total}</div>
        </Card>
        <Select
          value={statusFilter}
          onChange={setStatusFilter}
          placeholder="全部状态"
          allowClear
          style={{ width: 160 }}
          options={[
            { value: 'pending', label: '排队中' },
            { value: 'running', label: '生成中' },
            { value: 'success', label: '完成' },
            { value: 'failed', label: '失败' },
            { value: 'cancelled', label: '已取消' },
          ]}
        />
        <Button icon={<ReloadOutlined />} onClick={() => fetch()} loading={loading}>
          刷新
        </Button>
      </Space>

      <Table
        size="small"
        loading={loading}
        dataSource={list}
        rowKey="task_id"
        pagination={{ pageSize: 20, showSizeChanger: true }}
        scroll={{ x: 1300 }}
        columns={[
          {
            title: 'TaskID',
            dataIndex: 'task_id',
            width: 140,
            fixed: 'left',
            render: (id) => (
              <Tooltip title={id}>
                <code style={{ fontSize: 11 }}>{id.slice(0, 12)}...</code>
              </Tooltip>
            ),
          },
          { title: '用户', dataIndex: 'username', width: 140, fixed: 'left' },
          { title: '周期', dataIndex: 'period', width: 90 },
          {
            title: '格式',
            dataIndex: 'formats',
            width: 100,
            render: (f) => <Tag color="blue">{f || '-'}</Tag>,
          },
          {
            title: '状态',
            dataIndex: 'status',
            width: 110,
            filters: [
              { text: '排队中', value: 'pending' },
              { text: '生成中', value: 'running' },
              { text: '完成', value: 'success' },
              { text: '失败', value: 'failed' },
              { text: '已取消', value: 'cancelled' },
            ],
            onFilter: (val, row) => row.status === val,
            render: (s) => (
              <Tag icon={STATUS_ICON[s]} color={STATUS_COLOR[s]}>
                {STATUS_LABEL[s] || s}
              </Tag>
            ),
          },
          {
            title: '进度',
            dataIndex: 'progress',
            width: 130,
            render: (p, row) => {
              if (row.status === 'running') {
                return <Progress percent={Math.max(p, 5)} size="small" status="active" />
              }
              if (row.status === 'success') return <Progress percent={100} size="small" status="success" />
              if (row.status === 'failed') return <Progress percent={p} size="small" status="exception" />
              if (row.status === 'cancelled') return <Progress percent={p} size="small" status="normal" />
              return <Progress percent={p} size="small" />
            },
          },
          {
            title: '文件大小',
            dataIndex: 'file_size',
            width: 100,
            sorter: (a, b) => a.file_size - b.file_size,
            render: (s) => formatSize(s),
          },
          {
            title: '操作人',
            dataIndex: 'operator',
            width: 100,
          },
          {
            title: '创建时间',
            dataIndex: 'created_at',
            width: 160,
            sorter: (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
            render: (t) => formatTime(t),
          },
          {
            title: '完成时间',
            dataIndex: 'finished_at',
            width: 160,
            render: (t) => formatTime(t),
          },
          {
            title: '错误',
            dataIndex: 'error_msg',
            width: 200,
            ellipsis: true,
            render: (e) => e ? <span style={{ color: '#cf1322' }}>{e}</span> : '-',
          },
          {
            title: '操作',
            width: 180,
            fixed: 'right',
            render: (_, row) => (
              <Space size={4}>
                {row.status === 'success' && (
                  <Tooltip title="下载 ZIP">
                    <Button size="small" type="primary" icon={<DownloadOutlined />} onClick={() => onDownload(row)}>
                      下载
                    </Button>
                  </Tooltip>
                )}
                {row.status === 'pending' && canCancel && (
                  <Tooltip title="取消 (仅 pending 状态)">
                    <Button size="small" danger icon={<StopOutlined />} onClick={() => onCancel(row)}>
                      取消
                    </Button>
                  </Tooltip>
                )}
                {row.status === 'failed' && (
                  <Tooltip title={row.error_msg || '失败'}>
                    <Tag color="error" style={{ cursor: 'help' }}>失败</Tag>
                  </Tooltip>
                )}
              </Space>
            ),
          },
        ]}
      />

      <div style={{ marginTop: 16, fontSize: 12, color: '#999' }}>
        轮询策略: 当有 running/pending 任务时 5s 自动刷新, 否则停止轮询避免无效请求。
        30 天清理由后端 cron 任务 (PR #7) 处理。
      </div>
    </div>
  )
}
