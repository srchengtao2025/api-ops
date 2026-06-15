import { useEffect, useState } from 'react'
import { Button, Form, Input, Modal, Popconfirm, Space, Table, Tag, message } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { api, UpstreamVendor } from '../api'

export default function Vendors() {
  const [list, setList] = useState<UpstreamVendor[]>([])
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<UpstreamVendor | null>(null)
  const [form] = Form.useForm()

  const fetch = async () => {
    const data = (await api.listVendors()) as UpstreamVendor[]
    setList(data || [])
  }
  useEffect(() => { fetch() }, [])

  const onCreate = () => {
    setEditing(null)
    form.resetFields()
    setOpen(true)
  }
  const onEdit = (v: UpstreamVendor) => {
    setEditing(v)
    form.setFieldsValue(v)
    setOpen(true)
  }
  const onDelete = async (id: number) => {
    await api.deleteVendor(id)
    message.success('已删除')
    fetch()
  }
  const onSubmit = async () => {
    const values = await form.validateFields()
    if (editing) {
      await api.updateVendor(editing.id, values)
    } else {
      await api.createVendor(values)
    }
    message.success('已保存')
    setOpen(false)
    fetch()
  }

  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={onCreate}>新增供应商</Button>
      </Space>
      <Table
        rowKey="id"
        dataSource={list}
        columns={[
          { title: '代码', dataIndex: 'code' },
          { title: '名称', dataIndex: 'name' },
          { title: '联系人', dataIndex: 'contact_name' },
          { title: '邮箱', dataIndex: 'contact_email' },
          { title: '电话', dataIndex: 'contact_phone' },
          { title: '结算周期', dataIndex: 'billing_cycle', render: (v) => <Tag>{v || 'monthly'}</Tag> },
          { title: '备注', dataIndex: 'remark' },
          {
            title: '操作',
            render: (_, r) => (
              <Space>
                <Button size="small" onClick={() => onEdit(r)}>编辑</Button>
                <Popconfirm title="确认删除？" onConfirm={() => onDelete(r.id)}>
                  <Button size="small" danger>删除</Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal open={open} title={editing ? '编辑供应商' : '新增供应商'} onCancel={() => setOpen(false)} onOk={onSubmit} destroyOnClose>
        <Form form={form} layout="vertical">
          <Form.Item name="code" label="代码" rules={[{ required: true }]}>
            <Input placeholder="如 openai-azure" />
          </Form.Item>
          <Form.Item name="name" label="名称" rules={[{ required: true }]}>
            <Input placeholder="如 微软 Azure OpenAI" />
          </Form.Item>
          <Form.Item name="contact_name" label="联系人"><Input /></Form.Item>
          <Form.Item name="contact_email" label="邮箱"><Input /></Form.Item>
          <Form.Item name="contact_phone" label="电话"><Input /></Form.Item>
          <Form.Item name="billing_cycle" label="结算周期" initialValue="monthly">
            <Input placeholder="monthly / weekly / custom" />
          </Form.Item>
          <Form.Item name="remark" label="备注"><Input.TextArea rows={2} /></Form.Item>
        </Form>
      </Modal>
    </div>
  )
}