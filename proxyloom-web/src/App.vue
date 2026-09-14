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
      <nav v-if="auth.user" aria-label="主要导航"><RouterLink to="/overview">概览</RouterLink><RouterLink to="/nodes" :class="{ active: route.path.startsWith('/nodes') }">节点</RouterLink><RouterLink to="/sources" :class="{ active: route.path.startsWith('/sources') }">来源</RouterLink><RouterLink to="/imports" :class="{ active: route.path.startsWith('/imports') }">导入</RouterLink><RouterLink to="/chains" :class="{ active: route.path.startsWith('/chains') }">链路</RouterLink><RouterLink to="/policy-groups" :class="{ active: route.path.startsWith('/policy-groups') }">策略组</RouterLink><RouterLink to="/routing" :class="{ active: route.path.startsWith('/routing') }">路由</RouterLink><RouterLink to="/rule-sets" :class="{ active: route.path.startsWith('/rule-sets') }">规则集</RouterLink><RouterLink to="/dns" :class="{ active: route.path.startsWith('/dns') }">DNS</RouterLink><RouterLink to="/subscriptions" :class="{ active: route.path.startsWith('/subscriptions') }">订阅</RouterLink><RouterLink to="/client-presets" :class="{ active: route.path.startsWith('/client-presets') }">客户端预设</RouterLink><RouterLink to="/tests" :class="{ active: route.path === '/tests' }">测试</RouterLink><RouterLink to="/test-targets" :class="{ active: route.path === '/test-targets' }">测试目标</RouterLink><RouterLink to="/test-results" :class="{ active: route.path === '/test-results' }">测试历史</RouterLink><RouterLink to="/jobs" :class="{ active: route.path.startsWith('/jobs') }">任务</RouterLink><RouterLink to="/cores">内核</RouterLink><RouterLink to="/system">系统</RouterLink></nav>
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
