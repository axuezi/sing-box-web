import { ref } from 'vue'
import { api, ApiError, post, setCsrfToken, type Me } from '../api'

export const me = ref<Me | null>(null)
/** Set when the server has no administrator yet. */
export const setupRequired = ref(false)

function adopt(m: Me | null) {
  me.value = m
  setCsrfToken(m?.csrf_token ?? '')
}

/** Loads the current session; resolves to false when signed out. */
export async function loadSession(): Promise<boolean> {
  try {
    adopt(await api<Me>('/me'))
    setupRequired.value = false
    return true
  } catch (e) {
    adopt(null)
    setupRequired.value = e instanceof ApiError && e.code === 'setup_required'
    return false
  }
}

export async function login(username: string, password: string, code = '') {
  adopt(await post<Me>('/login', { username, password, code }))
}

export async function logout() {
  try {
    await post('/logout')
  } finally {
    adopt(null)
  }
}

export function forgetSession() {
  adopt(null)
}
