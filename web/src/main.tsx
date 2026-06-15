import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { ConfigProvider, theme } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import 'dayjs/locale/zh-cn'
import './styles.css' // 全局 demo 风格 (深空黑 + 电光蓝)
import App from './App'
import Login from './pages/Login'
import { getToken } from './api'

function PrivateRoute({ children }: { children: React.ReactNode }) {
  if (!getToken()) {
    return <Navigate to="/login" replace />
  }
  return <>{children}</>
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ConfigProvider
      locale={zhCN}
      theme={{
        algorithm: theme.darkAlgorithm,
        token: {
          colorPrimary: '#3B82F6',
          colorBgBase: '#0B0E14',
          colorBgContainer: '#0F1729',
          colorBgElevated: '#131B30',
          colorBgLayout: '#0B0E14',
          colorBorder: '#2A3346',
          colorBorderSecondary: '#1F2937',
          colorText: '#E5E7EB',
          colorTextSecondary: '#9CA3AF',
          colorTextTertiary: '#6B7280',
          colorSuccess: '#10B981',
          colorWarning: '#F59E0B',
          colorError: '#EF4444',
          colorInfo: '#3B82F6',
          borderRadius: 6,
          fontFamily: 'Inter, "Source Han Sans SC", "PingFang SC", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
        },
        components: {
          Layout: {
            headerBg: '#0F1729',
            bodyBg: '#0B0E14',
            siderBg: '#0F1729',
          },
          Menu: {
            itemBg: 'transparent',
            itemColor: '#9CA3AF',
            itemHoverColor: '#E5E7EB',
            itemSelectedBg: 'rgba(59, 130, 246, 0.12)',
            itemSelectedColor: '#3B82F6',
            itemActiveBg: 'rgba(59, 130, 246, 0.18)',
          },
          Card: {
            colorBgContainer: '#0F1729',
            colorBorderSecondary: '#2A3346',
          },
          Table: {
            colorBgContainer: '#0F1729',
            headerBg: '#131B30',
            headerColor: '#9CA3AF',
            rowHoverBg: 'rgba(59, 130, 246, 0.06)',
          },
          Button: {
            colorPrimary: '#3B82F6',
            colorPrimaryHover: '#60A5FA',
            defaultBg: '#0F1729',
            defaultColor: '#E5E7EB',
            defaultBorderColor: '#2A3346',
          },
          Tabs: {
            itemColor: '#9CA3AF',
            itemHoverColor: '#E5E7EB',
            itemSelectedColor: '#3B82F6',
            inkBarColor: '#3B82F6',
          },
          Statistic: {
            titleFontSize: 12,
            contentFontSize: 28,
            colorTextDescription: '#9CA3AF',
          },
        },
      }}
    >
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/*" element={<PrivateRoute><App /></PrivateRoute>} />
        </Routes>
      </BrowserRouter>
    </ConfigProvider>
  </React.StrictMode>,
)
