import { useEffect, useState } from 'react'
import {
  Button,
  Form,
  InputNumber,
  Modal,
  Select,
  Space,
  Statistic,
  Table,
  Tag,
  Tooltip,
  message,
  Input,
} from 'antd'
import { ReloadOutlined, EditOutlined, LinkOutlined, ThunderboltOutlined, SearchOutlined } from '@ant-design/icons'
import { http, getUser } from '../api'

interface Vendor {
  id: number
  code: string
  name: string
  contact_name?: string
  contact_email?: string
  billing_cycle?: string
  remark?: string
}

interface ChannelMapping {
  channel_id: number
  channel_name: string
  channel_type: number
  channel_status: number
  channel_group: string
  channel_balance: number
  mapping_id: number
  vendor_code: string
  vendor_name: string
  discount: number
  auto_discount: number
  auto_matched: string
  auto_recognized: boolean
  discount_override: boolean
  remark: string
}

export default function VendorManagement() {
  const [list, setList] = useState<ChannelMapping[]>([])
  const [vendors, setVendors] = useState<Vendor[]>([])
  const [loading, setLoading] = useState(false)
  const [search, setSearch] = useState('')
  const [filterVendor, setFilterVendor] = useState<string | undefined>(undefined)
  const [filterStatus, setFilterStatus] = useState<'all' | 'mapped' | 'unmapped' | 'unrecognized'>('all')
  const [assignOpen, setAssignOpen] = useState(false)
  const [editing, setEditing] = useState<ChannelMapping | null>(null)
  const [correctOpen, setCorrectOpen] = useState(false)
  const [correcting, setCorrecting] = useState<ChannelMapping | null>(null)
  const [assignForm] = Form.useForm()
  const [correctForm] = Form.useForm()
  const me = getUser()
  const canEdit = me?.role === 'admin' || me?.role === 'finance'
  const canReparse = me?.role === 'admin'

  const fetchAll = async () => {
    setLoading(true)
    try {
      const [m, v] = await Promise.all([
        http.get<{ items: ChannelMapping[]; total: number }>('/channel-mappings'),
        http.get<Vendor[]>('/vendors'),
      ])
      setList((m as any).items || [])
      setVendors((v as any) || [])
    } catch (e: any) {
      message.error('加载失败: ' + e.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchAll()
  }, [])

  const onAssign = (row: ChannelMapping) => {
    setEditing(row)
    assignForm.setFieldsValue({
      vendor_code: row.vendor_code || undefined,
      discount: row.discount,
      remark: row.remark,
    })
    setAssignOpen(true)
  }

  const onAssignSubmit = async () => {
    if (!editing) return
    const v = await assignForm.validateFields()
    try {
      await http.post('/channel-mappings', {
        channel_id: editing.channel_id,
        vendor_code: v.vendor_code,
        discount: v.discount,
        remark: v.remark || '',
      })
      message.success(`已分配 channel #${editing.channel_id} 到 ${v.vendor_code}`)
      setAssignOpen(false)
      fetchAll()
    } catch (e: any) {
      message.error('分配失败: ' + e.message)
    }
  }

  const onCorrect = (row: ChannelMapping) => {
    setCorrecting(row)
    correctForm.setFieldsValue({
      discount: row.discount,
      remark: row.remark,
    })
    setCorrectOpen(true)
  }

  const onCorrectSubmit = async () => {
    if (!correcting) return
    const v = await correctForm.validateFields()
    try {
      await http.post(`/channel-mappings/${correcting.channel_id}/correct-discount`, {
        discount: v.discount,
        remark: v.remark || '',
      })
      message.success(`已矫正 channel #${correcting.channel_id} 折扣为 ${v.discount}`)
      setCorrectOpen(false)
      fetchAll()
    } catch (e: any) {
      message.error('矫正失败: ' + e.message)
    }
  }

  const onUnassign = async (row: ChannelMapping) => {
    Modal.confirm({
      title: `解除 channel #${row.channel_id} 的供应商映射?`,
      content: `${row.channel_name}  (当前: ${row.vendor_name || row.vendor_code || '未归类'})`,
      okText: '解除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        try {
          await http.delete(`/channel-mappings/${row.channel_id}`)
          message.success('已解除')
          fetchAll()
        } catch (e: any) {
          message.error('解除失败: ' + e.message)
        }
      },
    })
  }

  const onReparse = async () => {
    Modal.confirm({
      title: '重跑所有渠道的折扣自动解析?',
      content: '不会覆盖已人工矫正的渠道 (discount_override=true)',
      okText: '开始重跑',
      cancelText: '取消',
      onOk: async () => {
        try {
          const r: any = await http.post('/channel-mappings/reparse', {})
          message.success(`重跑完成: 更新了 ${r?.updated} 条`)
          fetchAll()
        } catch (e: any) {
          message.error('重跑失败: ' + e.message)
        }
      },
    })
  }

  // 过滤
  const filtered = list.filter((row) => {
    if (search && !row.channel_name.toLowerCase().includes(search.toLowerCase()) && !String(row.channel_id).includes(search)) {
      return false
    }
    if (filterVendor && row.vendor_code !== filterVendor) return false
    if (filterStatus === 'mapped' && !row.vendor_code) return false
    if (filterStatus === 'unmapped' && row.vendor_code) return false
    if (filterStatus === 'unrecognized' && row.auto_recognized) return false
    return true
  })

  // 统计
  const totalMapped = list.filter((r) => r.vendor_code).length
  const totalUnmapped = list.length - totalMapped
  const totalUnrecognized = list.filter((r) => !r.auto_recognized).length

  return (
    <div>
      <h2>供应商管理 - 渠道配置</h2>
      <p style={{ color: '#888' }}>
        自动从 upstream 站点拉取 49 个渠道, 按渠道名末尾的"数字+折"自动解析折扣。
        解析不正确的可手动矫正 (矫正后不会再被自动覆盖)。
      </p>

      <Space size="large" style={{ marginBottom: 16 }}>
        <Statistic title="总渠道" value={list.length} />
        <Statistic title="已映射" value={totalMapped} valueStyle={{ color: '#3f8600' }} />
        <Statistic title="未映射" value={totalUnmapped} valueStyle={{ color: '#cf1322' }} />
        <Statistic title="未识别折扣" value={totalUnrecognized} valueStyle={{ color: '#faad14' }} />
      </Space>

      <Space style={{ marginBottom: 12 }} wrap>
        <Input
          placeholder="搜索渠道名 / ID"
          prefix={<SearchOutlined />}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          style={{ width: 240 }}
          allowClear
        />
        <Select
          placeholder="供应商"
          value={filterVendor}
          onChange={setFilterVendor}
          allowClear
          style={{ width: 160 }}
          options={[
            { value: '', label: '全部供应商' },
            ...vendors.map((v) => ({ value: v.code, label: `${v.code} - ${v.name}` })),
          ]}
        />
        <Select
          value={filterStatus}
          onChange={(v) => setFilterStatus(v as any)}
          style={{ width: 140 }}
          options={[
            { value: 'all', label: '全部状态' },
            { value: 'mapped', label: '已映射' },
            { value: 'unmapped', label: '未映射' },
            { value: 'unrecognized', label: '⚠️ 未识别' },
          ]}
        />
        <Button icon={<ReloadOutlined />} onClick={fetchAll} loading={loading}>
          刷新
        </Button>
        {canReparse && (
          <Button icon={<ThunderboltOutlined />} onClick={onReparse}>
            重跑自动解析
          </Button>
        )}
      </Space>

      <Table
        size="small"
        loading={loading}
        dataSource={filtered}
        rowKey="channel_id"
        pagination={{ pageSize: 20, showSizeChanger: true }}
        scroll={{ x: 1200 }}
        columns={[
          {
            title: '渠道ID',
            dataIndex: 'channel_id',
            width: 70,
            fixed: 'left',
            sorter: (a, b) => a.channel_id - b.channel_id,
          },
          {
            title: '渠道名',
            dataIndex: 'channel_name',
            width: 240,
            fixed: 'left',
            render: (n) => <span style={{ fontSize: 12 }}>{n}</span>,
          },
          {
            title: '供应商',
            dataIndex: 'vendor_name',
            width: 160,
            render: (name, row) =>
              name ? (
                <Tooltip title={`code: ${row.vendor_code}`}>
                  <Tag color="blue">{name}</Tag>
                </Tooltip>
              ) : (
                <Tag color="default">⚠️ 未归类</Tag>
              ),
          },
          {
            title: '自动折扣',
            dataIndex: 'auto_discount',
            width: 110,
            sorter: (a, b) => a.auto_discount - b.auto_discount,
            render: (d, row) => (
              <Tooltip title={row.auto_matched || '未匹配到'}>
                {row.auto_recognized ? (
                  <span style={{ color: '#52c41a' }}>{(d * 100).toFixed(1)}%</span>
                ) : (
                  <Tag color="warning">⚠️ 未识别</Tag>
                )}
              </Tooltip>
            ),
          },
          {
            title: '最终折扣',
            dataIndex: 'discount',
            width: 110,
            sorter: (a, b) => a.discount - b.discount,
            render: (d, row) => (
              <Space size={4}>
                <span style={{ fontWeight: 600 }}>{(d * 100).toFixed(1)}%</span>
                {row.discount_override && <Tag color="orange">人工</Tag>}
              </Space>
            ),
          },
          {
            title: '矫正原因',
            dataIndex: 'remark',
            ellipsis: true,
            render: (r) => (r ? <span style={{ color: '#666' }}>{r}</span> : '-'),
          },
          {
            title: '操作',
            width: 200,
            fixed: 'right',
            render: (_, row) => (
              <Space size={4}>
                {canEdit && (
                  <>
                    <Button
                      size="small"
                      type={row.vendor_code ? 'default' : 'primary'}
                      icon={<LinkOutlined />}
                      onClick={() => onAssign(row)}
                    >
                      {row.vendor_code ? '换供应商' : '分配'}
                    </Button>
                    <Button
                      size="small"
                      icon={<EditOutlined />}
                      onClick={() => onCorrect(row)}
                      disabled={!row.vendor_code}
                    >
                      矫正
                    </Button>
                    {row.vendor_code && (
                      <Button size="small" danger onClick={() => onUnassign(row)}>
                        解除
                      </Button>
                    )}
                  </>
                )}
              </Space>
            ),
          },
        ]}
      />

      {/* 分配 / 换供应商 */}
      <Modal
        title={editing ? `分配 channel #${editing.channel_id} 给供应商` : '分配'}
        open={assignOpen}
        onCancel={() => setAssignOpen(false)}
        onOk={onAssignSubmit}
        okText="保存"
        cancelText="取消"
        destroyOnClose
      >
        {editing && (
          <Form form={assignForm} layout="vertical" preserve={false}>
            <Form.Item label="渠道">
              <Input value={editing.channel_name} disabled />
            </Form.Item>
            <Form.Item
              name="vendor_code"
              label="供应商"
              rules={[{ required: true, message: '请选择供应商' }]}
            >
              <Select
                showSearch
                placeholder="选择供应商"
                optionFilterProp="label"
                options={vendors.map((v) => ({
                  value: v.code,
                  label: `${v.code} - ${v.name}`,
                }))}
              />
            </Form.Item>
            <Form.Item
              name="discount"
              label="折扣 (0-1)"
              extra="0.42 = 42% 原价. 留 0 = 用自动解析"
              rules={[{ required: true, type: 'number', min: 0, max: 1 }]}
            >
              <InputNumber step={0.01} min={0} max={1} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item name="remark" label="备注">
              <Input placeholder="可选, 如: 主供应商 / 备节点" />
            </Form.Item>
          </Form>
        )}
      </Modal>

      {/* 矫正折扣 */}
      <Modal
        title={correcting ? `矫正 channel #${correcting.channel_id} 的折扣` : '矫正'}
        open={correctOpen}
        onCancel={() => setCorrectOpen(false)}
        onOk={onCorrectSubmit}
        okText="保存 (标记人工矫正)"
        cancelText="取消"
        destroyOnClose
      >
        {correcting && (
          <Form form={correctForm} layout="vertical" preserve={false}>
            <Form.Item label="渠道">
              <Input value={correcting.channel_name} disabled />
            </Form.Item>
            <Form.Item label="自动解析">
              <Input
                value={`${(correcting.auto_discount * 100).toFixed(1)}% (${
                  correcting.auto_matched || '未识别'
                })`}
                disabled
              />
            </Form.Item>
            <Form.Item
              name="discount"
              label="折扣 (0-1)"
              rules={[{ required: true, type: 'number', min: 0, max: 1 }]}
            >
              <InputNumber step={0.01} min={0} max={1} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item name="remark" label="矫正原因">
              <Input placeholder="例: 75 折实际是 75% 而非 7.5%" />
            </Form.Item>
          </Form>
        )}
      </Modal>
    </div>
  )
}
