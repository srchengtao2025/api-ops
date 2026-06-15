import axios from 'axios'

// ===== A 阶段: JWT token 持久化 (localStorage) =====
const TOKEN_KEY = 'api_ops_token'
const USER_KEY = 'api_ops_user'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}
export function setToken(t: string) {
  localStorage.setItem(TOKEN_KEY, t)
}
export function clearToken() {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(USER_KEY)
}

export interface AuthUser {
  user_id: number
  username: string
  display_name: string
  role: 'admin' | 'finance' | 'viewer'
}
export function getUser(): AuthUser | null {
  const raw = localStorage.getItem(USER_KEY)
  if (!raw) return null
  try { return JSON.parse(raw) } catch { return null }
}
export function setUser(u: AuthUser) {
  localStorage.setItem(USER_KEY, JSON.stringify(u))
}

export const http = axios.create({
  baseURL: '/api',
  timeout: 30000,
})

// 请求拦截器: 自动加 JWT
http.interceptors.request.use((config) => {
  const t = getToken()
  if (t) {
    config.headers = config.headers || {}
    config.headers['Authorization'] = `Bearer ${t}`
  }
  return config
})

http.interceptors.response.use(
  (resp) => {
    const data = resp.data
    if (data && data.success === false) {
      return Promise.reject(new Error(data?.error?.message || '请求失败'))
    }
    return data?.data !== undefined ? data.data : data
  },
  (err) => {
    // 401 → 跳登录页
    if (err?.response?.status === 401) {
      clearToken()
      // 避免 /login 自身 401 时死循环
      if (window.location.pathname !== '/login') {
        window.location.href = '/login'
      }
    }
    const msg = err?.response?.data?.error || err?.response?.data?.error?.message || err.message
    return Promise.reject(new Error(msg))
  },
)

// ===== Auth API =====
export const authApi = {
  login: (username: string, password: string) =>
    http.post<{
      token: string
      token_type: string
      expires_in: number
      user_id: number
      username: string
      display_name: string
      role: 'admin' | 'finance' | 'viewer'
    }>('/auth/login', { username, password }),
  me: () => http.get<AuthUser>('/auth/me'),
  logout: () => http.post('/auth/logout'),
  changePassword: (old_password: string, new_password: string) =>
    http.post('/auth/change-password', { old_password, new_password }),
}

export interface ListResp<T> {
  total: number
  items: T[]
}

export interface UpstreamVendor {
  id: number
  code: string
  name: string
  contact_name?: string
  contact_email?: string
  contact_phone?: string
  billing_cycle?: string
  remark?: string
}

// UpstreamPricing interface 已下线 (2026-06-14), 成本反推改用渠道供应商折扣

// ===== BILLING v2 (PR #5 / 8, 2026-06-14) =====
//
// v1 BillingStatement interface 已下线 (2026-06-14):
//   - 后端对应 archive.billing_statements 表 (已归档到 archive schema)
//   - v2 用 V2CustomerMonthItem / V2ExportTask 替代
// v1 interface 移到 git history: 找 commit "PR: v1 billing 下线" 之前

export interface V2CustomerMonthItem {
  user_id: number
  username: string
  prompt_tokens: number
  completion_tokens: number
  cache_tokens: number
  cache_tokens: number
  revenue_usd: number
  request_count: number
}

export interface V2ExportTask {
  id: number
  task_id: string
  user_id: number
  username: string
  period: string
  formats: string
  kind?: 'customer' | 'upstream'        // BILLING v3 加 (PR #5)
  vendor_code?: string                 // BILLING v3 加 (PR #5)
  status: 'pending' | 'running' | 'success' | 'failed' | 'cancelled'
  progress: number
  file_path?: string
  file_size?: number
  error_msg?: string
  started_at?: string
  finished_at?: string
  created_at: string
  operator: string
}

// ===== BILLING v3 上游对账 SPA type (PR #5 / 7, 2026-06-14) =====

export interface V3UpstreamChannel {
  channel_id: number
  channel_name: string
  discount: number
  request_count: number
  total_cost: number
  total_revenue: number
  total_profit: number
  profit_rate: number
}

export interface V3UpstreamVendor {
  vendor_code: string
  vendor_name: string
  request_count: number
  total_cost: number
  total_revenue: number
  total_profit: number
  profit_rate: number
  channels: V3UpstreamChannel[]
}

// ===== BILLING v4 利润分析 SPA type (PR #5 / 6, 2026-06-14) =====

export interface V4ProfitByDay {
  date: string
  revenue: number
  cost: number
  profit: number
  request_count: number
}

export interface V4ProfitByUser {
  user_id: number
  username: string
  request_count: number
  prompt_tokens: number
  completion_tokens: number
  cache_tokens: number
  revenue: number
  cost: number
  profit: number
  profit_rate: number
}

export interface V4ProfitByVendor {
  vendor_code: string
  vendor_name: string
  request_count: number
  revenue: number
  cost: number
  profit: number
  profit_rate: number
}

export interface V4ProfitByModel {
  model_name: string
  request_count: number
  revenue: number
  cost: number
  profit: number
  profit_rate: number
}

export interface upstreamChannel {
  id: number
  name: string
  type: number
  status: number
  models: string
  group: string
  used_quota: number
  balance: number
  balance_updated_time: number
  response_time: number
}

export const api = {
  // vendors
  listVendors: () => http.get<UpstreamVendor[]>('/vendors'),
  createVendor: (v: Partial<UpstreamVendor>) => http.post<UpstreamVendor>('/vendors', v),
  updateVendor: (id: number, v: Partial<UpstreamVendor>) => http.put<UpstreamVendor>(`/vendors/${id}`, v),
  deleteVendor: (id: number) => http.delete(`/vendors/${id}`),
  listVendorChannels: (code: string) => http.get(`/vendors/${code}/channels`),

  // pricing 已下线 (2026-06-14), 4 个 API 方法全删
  // 成本反推改用渠道供应商折扣 (channel_vendor_map.discount)

  // channel-vendor mappings
  listChannelVendors: (channelId?: number) =>
    http.get(`/channel-vendors`, { params: { channel_id: channelId } }),
  upsertChannelVendor: (m: { channel_id: number; vendor_code: string; weight?: number; remark?: string }) =>
    http.post('/channel-vendors', m),
  deleteChannelVendor: (id: number) => http.delete(`/channel-vendors/${id}`),

  // upstream channels
  listupstreamChannels: () => http.get<upstreamChannel[]>('/upstream/channels'),

  // ===== BILLING v2 (PR #5 / 8, 2026-06-14) =====
  //
  // v1 客户对账 + 上游对账 + 利润分析 4 个 API 客户端方法已下线 (2026-06-14):
  //   - previewCustomerStatement / generateCustomerStatements / listCustomerStatements
  //   - getCustomerStatement / confirmStatement
  //   - generateUpstreamStatements / listUpstreamStatements / getUpstreamStatement
  //   - analyzeProfit
  // 后端路由 + handler + DB 表 + SPA 页面全删/归档, 调用方法无需保留.
  v2CurrentMonthOverview: () =>
    http.get<{
      period_start: number
      period_end: number
      user_count: number
      total_revenue: number
      total_tokens: number
      items: V2CustomerMonthItem[]
    }>('/billing/v2/customer/current-month-overview'),
  v2ExportLastMonth: (userId: number, req: { formats: string }) =>
    http.post<{ task_id: string; user_id: number; period: string; formats: string; status: string }>(
      `/billing/v2/customer/${userId}/export-last-month`,
      req,
    ),
  v2CustomerTasks: (userId: number, params: { status?: string; limit?: number } = {}) =>
    http.get<ListResp<V2ExportTask>>(`/billing/v2/customer/${userId}/tasks`, { params }),
  v2ExportTasks: (params: { status?: string; limit?: number } = {}) =>
    http.get<ListResp<V2ExportTask>>('/billing/v2/export-tasks', { params }),
  v2ExportDownloadUrl: (taskId: string) =>
    `/billing/v2/export-tasks/${taskId}/download`,
  v2CancelTask: (taskId: string) =>
    http.post<{ task_id: string; status: string; affected: number }>(`/billing/v2/export-tasks/${taskId}/cancel`, {}),

  // ===== BILLING v3 上游对账 (PR #5 / 7, 2026-06-14) =====
  v3UpstreamCurrentMonthOverview: () =>
    http.get<{
      period_start: number
      period_end: number
      vendor_count: number
      channel_count: number
      total_cost: number
      total_revenue: number
      total_profit: number
      items: V3UpstreamVendor[]
    }>('/billing/v3/upstream/current-month-overview'),
  v3UpstreamExportLastMonth: (req: { vendor_code?: string; formats: string }) =>
    http.post<{
      period: string
      vendor_count: number
      created: { task_id: string; vendor_code: string; period: string; status: string }[]
    }>('/billing/v3/upstream/export-last-month', req),
  v3UpstreamVendorTasks: (vendorCode: string, params: { status?: string; limit?: number; offset?: number } = {}) =>
    http.get<ListResp<V2ExportTask>>(`/billing/v3/upstream/${vendorCode}/tasks`, { params }),
  v3ExportTasks: (params: { status?: string; limit?: number; offset?: number } = {}) =>
    http.get<ListResp<V2ExportTask>>('/billing/v3/export-tasks', { params }),
  // 复用 v2 download + cancel (按 kind 路由, 同一 ZIP 路径)
  v3ExportDownloadUrl: (taskId: string) =>
    `/billing/v2/export-tasks/${taskId}/download`,
  v3CancelTask: (taskId: string) =>
    http.post<{ task_id: string; status: string; affected: number }>(`/billing/v2/export-tasks/${taskId}/cancel`, {}),

  // ===== BILLING v4 利润分析 (PR #5 / 6, 2026-06-14) =====
  v4ProfitOverview: () =>
    http.get<{
      period_start: number
      period_end: number
      user_count: number
      total_revenue: number
      total_cost: number
      total_profit: number
      profit_rate: number
      by_day: V4ProfitByDay[]
      by_user: V4ProfitByUser[]
      by_vendor: V4ProfitByVendor[]
      by_model: V4ProfitByModel[]
    }>('/billing/v4/profit/overview'),

  // dashboard (2026-06-14: 全 admin API + 砍 TopX 3 卡片, 只留 today)
  dashboardToday: () => http.get('/dashboard/today'),
  // 7 天趋势曲线 (不含今天, 2026-06-15 加回, admin 1 轮 7 调用 + 后端 cache 5min)
  dashboardTrend7d: () => http.get('/dashboard/trend-7d'),
  // TopX 3 个端点已禁用 (handlers_stmt.go + server.go):
  //   dashboardTopCustomers, dashboardTopModels, dashboardTopChannels
  // 恢复路径: 见 handlers_stmt.go 中 3 个 handler 函数的注释

  // monitor 渠道健康度 (2026-06-15 加, 后端 8 个端点 + 1min tick scheduler)
  monitorChannels: () => http.get<{ total: number; items: any[] }>('/monitor/channels'),
  monitorChannelHealth: (id: number, range: '1h' | '6h' | '24h' | '7d' = '1h') =>
    http.get<{ channel_id: number; granularity: string; count: number; items: any[] }>(`/monitor/channels/${id}/health`, { params: { range } }),
  // 告警中心 (3 个端点) —— 暂不开放 SPA, 仅 API 就绪
  // monitorAlerts, monitorAckAlert, monitorResolveAlert

  config: () => http.get('/config'),
}

// UpstreamPricingImport interface 已下线 (2026-06-14)
