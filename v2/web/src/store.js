import { reactive } from 'vue'
import { get } from './api.js'

export const session = reactive({ me: null, loaded: false })

export async function loadMe() {
  try { session.me = await get('/api/auth/me') } catch { session.me = null }
  session.loaded = true
  return session.me
}

export const isStaff = (u) => u && (u.role === 'operator' || u.role === 'super_admin')
