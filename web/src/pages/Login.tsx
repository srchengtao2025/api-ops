import { useState } from 'react'
import { Card, Form, Input, Button, Alert, Typography, Space } from 'antd'
import { LockOutlined, UserOutlined } from '@ant-design/icons'
import { authApi, setToken, setUser } from '../api'

const { Title, Text } = Typography

export default function Login() {
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function onFinish(values: { username: string; password: string }) {
    setLoading(true)
    setErr(null)
    try {
      const r = await authApi.login(values.username, values.password)
      setToken(r.token)
      setUser({
        user_id: r.user_id,
        username: r.username,
        display_name: r.display_name,
        role: r.role,
      })
      // 跳到首页 (App.tsx 会判 token)
      window.location.href = '/dashboard'
    } catch (e: any) {
      setErr(e?.message || '登录失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div
      style={{
        minHeight: '100vh',
        background: 'linear-gradient(135deg, #667eea 0%, #764ba2 100%)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 24,
      }}
    >
      <Card style={{ width: 420, boxShadow: '0 10px 40px rgba(0,0,0,0.15)' }}>
        <Space direction="vertical" size="large" style={{ width: '100%' }}>
          <div style={{ textAlign: 'center' }}>
            <Title level={3} style={{ marginBottom: 4 }}>🍥 upstream ops</Title>
            <Text type="secondary">运营管理系统</Text>
          </div>

          {err && <Alert type="error" message={err} showIcon closable />}

          <Form layout="vertical" onFinish={onFinish} autoComplete="off">
            <Form.Item
              name="username"
              label="用户名"
              rules={[{ required: true, message: '请输入用户名' }]}
            >
              <Input prefix={<UserOutlined />} placeholder="admin" size="large" autoFocus />
            </Form.Item>
            <Form.Item
              name="password"
              label="密码"
              rules={[{ required: true, message: '请输入密码' }]}
            >
              <Input.Password prefix={<LockOutlined />} placeholder="请输入密码" size="large" />
            </Form.Item>
            <Form.Item style={{ marginBottom: 0 }}>
              <Button type="primary" htmlType="submit" loading={loading} size="large" block>
                登录
              </Button>
            </Form.Item>
          </Form>

          <Text type="secondary" style={{ textAlign: 'center', display: 'block', fontSize: 12 }}>
            内部账号系统 · 3 角色: admin / finance / viewer
          </Text>
        </Space>
      </Card>
    </div>
  )
}
