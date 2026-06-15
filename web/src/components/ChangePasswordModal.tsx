import { useState } from 'react'
import { Modal, Form, Input, message } from 'antd'
import { authApi } from '../api'

interface Props {
  open: boolean
  onClose: () => void
}

export default function ChangePasswordModal({ open, onClose }: Props) {
  const [form] = Form.useForm()
  const [loading, setLoading] = useState(false)

  async function onOk() {
    try {
      const v = await form.validateFields()
      setLoading(true)
      await authApi.changePassword(v.old_password, v.new_password)
      message.success('密码已修改, 请重新登录')
      // 清除 token, 跳到 login
      localStorage.removeItem('api_ops_token')
      localStorage.removeItem('api_ops_user')
      setTimeout(() => {
        window.location.href = '/login'
      }, 1000)
    } catch (e: any) {
      if (e?.errorFields) return // 验证错误
      message.error(e?.message || '修改失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <Modal
      title="修改密码"
      open={open}
      onCancel={() => { form.resetFields(); onClose() }}
      onOk={onOk}
      confirmLoading={loading}
      okText="确认修改"
      cancelText="取消"
      destroyOnClose
    >
      <Form form={form} layout="vertical" preserve={false}>
        <Form.Item
          name="old_password"
          label="当前密码"
          rules={[{ required: true, message: '请输入当前密码' }]}
        >
          <Input.Password autoFocus />
        </Form.Item>
        <Form.Item
          name="new_password"
          label="新密码"
          rules={[
            { required: true, message: '请输入新密码' },
            { min: 8, message: '密码至少 8 位' },
          ]}
        >
          <Input.Password />
        </Form.Item>
        <Form.Item
          name="confirm"
          label="确认新密码"
          dependencies={['new_password']}
          rules={[
            { required: true, message: '请再次输入新密码' },
            ({ getFieldValue }) => ({
              validator(_, value) {
                if (!value || getFieldValue('new_password') === value) {
                  return Promise.resolve()
                }
                return Promise.reject(new Error('两次输入的密码不一致'))
              },
            }),
          ]}
        >
          <Input.Password />
        </Form.Item>
      </Form>
    </Modal>
  )
}
