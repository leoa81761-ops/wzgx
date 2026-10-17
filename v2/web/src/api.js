// Small fetch wrapper: JSON in/out, the session cookie rides along, errors carry the server's Chinese message.
import { router } from './router.js'

export async function api(method, path, body) {
  const opt = { method, headers: {}, credentials: 'same-origin' }
  if (body !== undefined) {
    opt.headers['Content-Type'] = 'application/json'
    opt.body = JSON.stringify(body)
  }
  const res = await fetch(path, opt)
  let data = null
  try { data = await res.json() } catch { /* empty body */ }
  if (res.status === 401 && !path.startsWith('/api/auth/')) {
    router.push({ path: '/login', query: { next: router.currentRoute.value.fullPath } })
  }
  if (!res.ok) throw new Error((data && data.error) || `请求失败（${res.status}）`)
  return data
}

export const get = (p) => api('GET', p)
export const post = (p, b) => api('POST', p, b ?? {})
export const put = (p, b) => api('PUT', p, b)
export const patch = (p, b) => api('PATCH', p, b)
export const del = (p) => api('DELETE', p)

export function fmtTime(t) {
  if (!t) return ''
  const d = new Date(t)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export const statusText = { ready: '可推送', blocked: '未通过检查', pushed: '已进草稿箱', draft: '草稿', failed: '失败' }
export const jobText = { queued: '排队中', running: '写作中', done: '完成', failed: '失败', cancelled: '已取消' }
export const weekNames = ['', '周一', '周二', '周三', '周四', '周五', '周六', '周日']
