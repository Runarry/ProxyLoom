<script setup lang="ts">
import { ref, watch } from 'vue'
import { RouterView, RouterLink, useRouter, useRoute } from 'vue-router'
import { useAuthStore } from './stores/auth'
import ErrorNotice from './components/ErrorNotice.vue'
import ReauthDialog from './components/ReauthDialog.vue'
import { localTime } from './api/client'
const auth = useAuthStore()
const router = useRouter()
const route = useRoute()
const error = ref<unknown>(null)
const loggingOut = ref(false)
watch(() => auth.user, (user, previous) => {
  if (!user && previous) { error.value = null; void router.replace({ name: 'login', query: { next: route.fullPath } }) }
})
async function logout() {
  loggingOut.value = true; error.value = null
  try { await auth.logout() } catch (failure) { error.value = failure } finally { loggingOut.value = false }
}
</script>

<template>
  <a class="skip-link" href="#main">跳转到主要内容</a>
  <div class="app-shell">
    <header class="site-header">
      <RouterLink class="brand" to="/"><span class="brand-mark" aria-hidden="true">P</span><span>ProxyLoom<small>织流 · 工作空间</small></span></RouterLink>
      <nav v-if="auth.user" aria-label="主要导航"><RouterLink to="/nodes" :class="{ active: route.path.startsWith('/nodes') }">节点</RouterLink><RouterLink to="/imports" :class="{ active: route.path.startsWith('/imports') }">导入</RouterLink></nav>
      <div v-if="auth.user" class="account"><span :title="`会话到期：${localTime(auth.user.session_expires_at)}`">{{ auth.user.username }}<small>管理员</small></span><button type="button" class="text-button" :disabled="loggingOut" @click="logout">退出</button></div>
      <span v-else class="header-note">节点管理</span>
    </header>
    <ErrorNotice :error="error" />
    <div v-if="!auth.ready" class="empty-state" role="status">正在恢复会话…</div>
    <RouterView v-else v-slot="{ Component }"><component :is="Component" :key="route.path" /></RouterView>
    <footer><span>ProxyLoom · 自托管订阅管理</span><span>时间按浏览器时区 {{ Intl.DateTimeFormat().resolvedOptions().timeZone }} 展示</span></footer>
  </div>
  <ReauthDialog v-if="auth.reauthOpen" />
</template>
