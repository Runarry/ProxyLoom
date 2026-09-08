import { ref } from 'vue'
import { defineStore } from 'pinia'
import { api, APIError, configureSession } from '../api/client'
import type { Schema } from '../api/client'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<Schema<'CurrentUser'> | null>(null)
  const ready = ref(false)
  const restoreError = ref<unknown>(null)
  const reauthOpen = ref(false)
  let restoring: Promise<void> | undefined
  let resolveReauth: ((value: boolean) => void) | undefined

  function clear() {
    user.value = null
    configureSession('')
    finishReauth(false)
  }
  function accept(value: Schema<'CurrentUser'>) {
    user.value = value
    configureSession(value.csrf_token, clear)
    restoreError.value = null
  }
  async function restore(force = false) {
    if (ready.value && !force) return
    if (restoring) return restoring
    restoring = (async () => {
      restoreError.value = null
      try { accept((await api<Schema<'SessionResponse'>>('/auth/me')).body.data) }
      catch (error) {
        clear()
        if (!(error instanceof APIError && error.status === 401)) restoreError.value = error
      } finally { ready.value = true; restoring = undefined }
    })()
    return restoring
  }
  async function login(request: Schema<'LoginRequest'>) {
    accept((await api<Schema<'SessionResponse'>>('/auth/login', { method: 'POST', body: request, anonymous: true })).body.data)
  }
  async function setup(request: Schema<'SetupRequest'>) {
    accept((await api<Schema<'SessionResponse'>>('/setup', { method: 'POST', body: request, anonymous: true })).body.data)
  }
  async function logout() {
    await api<Schema<'AcknowledgementResponse'>>('/auth/logout', { method: 'POST', body: {} })
    clear()
  }
  async function reauthenticate(password: string) {
    accept((await api<Schema<'SessionResponse'>>('/auth/reauth', { method: 'POST', body: { password } satisfies Schema<'ReauthenticationRequest'>, preserveSessionOn401: true })).body.data)
    finishReauth(true)
  }
  function requireRecentAuthentication(): Promise<boolean> {
    const until = user.value?.recent_authentication_expires_at
    if (until && new Date(until).getTime() > Date.now()) return Promise.resolve(true)
    finishReauth(false)
    reauthOpen.value = true
    return new Promise(resolve => { resolveReauth = resolve })
  }
  function finishReauth(success: boolean) {
    reauthOpen.value = false
    resolveReauth?.(success)
    resolveReauth = undefined
  }
  return { user, ready, restoreError, reauthOpen, restore, login, setup, logout, reauthenticate, requireRecentAuthentication, finishReauth }
})
