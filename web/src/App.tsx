import {
  DashboardOutlined,
  FileTextOutlined,
  DollarOutlined,
  TagsOutlined,
  LinkOutlined,
  MonitorOutlined,
  RobotOutlined,
  UserOutlined,
  LogoutOutlined,
  KeyOutlined,
  ShopOutlined,
  FileZipOutlined,
  FundOutlined,
} from '@ant-design/icons'
import { Routes, Route, Link, useLocation, Navigate, useNavigate } from 'react-router-dom'
import { useState } from 'react'
import { Dropdown, Tag, message } from 'antd'
import Dashboard from './pages/Dashboard'
import VendorManagement from './pages/VendorManagement'
// UpstreamPricing.tsx 已下线 (2026-06-14), 成本反推改用渠道供应商折扣
import Vendors from './pages/Vendors'
import BillingV2Customers from './pages/BillingV2Customers'
import BillingV2Exports from './pages/BillingV2Exports'
import BillingV3Upstream from './pages/BillingV3Upstream'
import BillingV4Profit from './pages/BillingV4Profit'
import ChannelHealth from './pages/ChannelHealth'
import ChangePasswordModal from './components/ChangePasswordModal'
import { authApi, getUser, clearToken, type AuthUser } from './api'

const ROLE_COLOR: Record<string, string> = {
  admin: 'red',
  finance: 'blue',
  viewer: 'default',
}
const ROLE_LABEL: Record<string, string> = {
  admin: '管理员',
  finance: '财务',
  viewer: '只读',
}

// 菜单项 + 路径 -> 面包屑段
type NavItem = { path: string; label: string; icon: React.ReactNode; group?: string }
const NAV_ITEMS: NavItem[] = [
  { path: '/dashboard', label: '总览看板', icon: <DashboardOutlined />, group: '总览' },
  { path: '/billing/v2/customer', label: '客户账单 (v2)', icon: <FileTextOutlined />, group: '对账中心' },
  { path: '/billing/v3/upstream', label: '上游对账 (v3)', icon: <ShopOutlined />, group: '对账中心' },
  { path: '/billing/v4/profit', label: '利润分析 (v4)', icon: <FundOutlined />, group: '对账中心' },
  { path: '/billing/exports', label: '任务中心', icon: <FileZipOutlined />, group: '对账中心' },
  { path: '/vendor/channels', label: '渠道供应商', icon: <LinkOutlined />, group: '供应商管理' },
  { path: '/vendor/vendors', label: '供应商档案', icon: <ShopOutlined />, group: '供应商管理' },
  { path: '/monitor/channels', label: '渠道健康', icon: <MonitorOutlined />, group: '监控中心' },
]

const GROUPS = ['总览', '对账中心', '供应商管理', '监控中心'] as const

export default function App() {
  const location = useLocation()
  const navigate = useNavigate()
  const [me, setMe] = useState<AuthUser | null>(getUser())
  const [pwdOpen, setPwdOpen] = useState(false)

  async function handleLogout() {
    try {
      await authApi.logout()
    } catch {
      // ignore
    }
    clearToken()
    message.success('已登出')
    navigate('/login', { replace: true })
  }

  // 当前路径最匹配的 nav item (处理 /billing/exports 等子路径)
  const currentNav = (() => {
    let best: NavItem | null = null
    for (const it of NAV_ITEMS) {
      if (location.pathname === it.path || location.pathname.startsWith(it.path + '/')) {
        if (!best || it.path.length > best.path.length) best = it
      }
    }
    return best
  })()

  const now = new Date()
  const pad = (n: number) => n.toString().padStart(2, '0')
  const clock = `${pad(now.getHours())}:${pad(now.getMinutes())}:${pad(now.getSeconds())}`

  return (
    <>
    <div className="app-layout">
      {/* Sidebar */}
      <aside className="app-sidebar">
        <div className="logo">
          <div className="logo-mark">R</div>
          <div>
            <div className="logo-text">upstream ops</div>
            <div className="logo-sub">运营驾驶舱 v1.0</div>
          </div>
        </div>
        {GROUPS.map((g) => {
          const items = NAV_ITEMS.filter((it) => it.group === g)
          if (!items.length) return null
          return (
            <div key={g}>
              <div className="nav-section">{g}</div>
              {items.map((it) => (
                <Link
                  key={it.path}
                  to={it.path}
                  className={`nav-item${currentNav?.path === it.path ? ' active' : ''}`}
                >
                  <span className="nav-icon">{it.icon}</span>
                  <span>{it.label}</span>
                </Link>
              ))}
            </div>
          )
        })}
        <div className="nav-section">规划中</div>
        <span className="nav-item" style={{ opacity: 0.4, cursor: 'not-allowed' }}>
          <span className="nav-icon"><RobotOutlined /></span><span>AI 分析</span>
        </span>
      </aside>

      {/* Header */}
      <header className="app-header">
        <div className="breadcrumb">
          <Link to="/dashboard">api-ops</Link>
          <span className="sep">/</span>
          {currentNav?.group && (
            <>
              <span>{currentNav.group}</span>
              <span className="sep">/</span>
            </>
          )}
          <span className="current">{currentNav?.label || '页面'}</span>
        </div>
        <div className="header-right">
          <span className="env-badge">
            <span className="status-dot success" />
            公网 · api-ops.example.com
          </span>
          <span style={{ fontSize: 12, color: 'var(--text-tertiary)', fontVariantNumeric: 'tabular-nums' }}>{clock}</span>
          {me && (
            <Dropdown
              menu={{
                items: [
                  { key: 'profile', label: <span><UserOutlined /> {me.display_name || me.username}</span>, disabled: true },
                  { type: 'divider' as const },
                  { key: 'change-pwd', label: <span><KeyOutlined /> 修改密码</span>, onClick: () => setPwdOpen(true) },
                  { key: 'logout', label: <span><LogoutOutlined /> 退出登录</span>, danger: true, onClick: handleLogout },
                ],
              }}
            >
              <span className="user-chip" style={{ cursor: 'pointer' }}>
                <span className="avatar">{(me.display_name || me.username).slice(0, 2).toUpperCase()}</span>
                <span>{me.display_name || me.username}</span>
                <Tag color={ROLE_COLOR[me.role] || 'default'} style={{ marginLeft: 4 }}>{ROLE_LABEL[me.role] || me.role}</Tag>
              </span>
            </Dropdown>
          )}
        </div>
      </header>

      {/* Main */}
      <main className="app-main">
        <Routes>
          <Route path="/" element={<Navigate to="/dashboard" replace />} />
          <Route path="/dashboard" element={<Dashboard />} />
          <Route path="/vendor/channels" element={<VendorManagement />} />
          <Route path="/vendor/vendors" element={<Vendors />} />
          <Route path="/billing/v2/customer" element={<BillingV2Customers />} />
          <Route path="/billing/v2/exports" element={<BillingV2Exports />} />
          <Route path="/billing/exports" element={<BillingV2Exports />} />
          <Route path="/billing/v3/upstream" element={<BillingV3Upstream />} />
          <Route path="/billing/v4/profit" element={<BillingV4Profit />} />
          <Route path="/monitor/channels" element={<ChannelHealth />} />
          <Route path="*" element={<div className="ops-card" style={{ textAlign: 'center', color: 'var(--text-tertiary)', padding: 60 }}>该模块尚未实现（计划中）</div>} />
        </Routes>
      </main>
    </div>
    <ChangePasswordModal open={pwdOpen} onClose={() => setPwdOpen(false)} />
    </>
  )
}
