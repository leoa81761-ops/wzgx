import { reactive } from 'vue'
import { get } from './api.js'

export const session = reactive({ me: null, loaded: false, notice: '' })

// a random id kept in this browser, sent at registration (invite anti-abuse only)
export function deviceId() {
  let d = localStorage.getItem('gh_device')
  if (!d) { d = Array.from(crypto.getRandomValues(new Uint8Array(12)), (b) => b.toString(16).padStart(2, '0')).join(''); localStorage.setItem('gh_device', d) }
  return d
}

export async function loadMe() {
  try { session.me = await get('/api/auth/me') } catch { session.me = null }
  session.loaded = true
  return session.me
}

export const isStaff = (u) => u && (u.role === 'operator' || u.role === 'super_admin')
