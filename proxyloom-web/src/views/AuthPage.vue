<script setup lang="ts">
import { ref, computed, onBeforeUnmount, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import ErrorNotice from '../components/ErrorNotice.vue'
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const initializing = computed(() => route.name === 'setup')
const username = ref('')
const password = ref('')
const setupToken = ref('')
const error = ref<unknown>(null)
const busy = ref(false)
watch(initializing, () => { password.value = ''; setupToken.value = ''; error.value = null })
onBeforeUnmount(() => { password.value = ''; setupToken.value = '' })
async function retryRestore() {
  await auth.restore(true)
  if (auth.user) {
    const destination = typeof route.query.next === 'string' && route.query.next.startsWith('/') && !route.query.next.startsWith('//') ? route.query.next : '/nodes'
    await router.replace(destination)
  }
}
async function submit() {
  busy.value = true; error.value = null
  try {
    if (initializing.value) await auth.setup({ username: username.value, password: password.value, setup_token: setupToken.value })
    else await auth.login({ username: username.value, password: password.value })
    const destination = typeof route.query.next === 'string' && route.query.next.startsWith('/') && !route.query.next.startsWith('//') ? route.query.next : '/nodes'
    await router.replace(destination)
  } catch (failure) { error.value = failure } finally { password.value = ''; setupToken.value = ''; busy.value = false }
}
</script>

<template>
  <main id="main" class="auth-page">
    <div class="auth-intro"><p class="eyebrow">自托管 · 单工作空间</p><h1>把每一个连接，<br />整理清楚。</h1><p>管理节点与导入记录，让配置的每次变更都有迹可循。</p><div class="auth-note"><span class="badge warning">兼容性需独立验证</span><p>节点保存和分享链接解析成功，不代表真实内核验证通过。</p></div></div>
    <section class="panel auth-card">
      <p class="eyebrow">PROXYLOOM / 管理员</p><h2>{{ initializing ? '初始化工作空间' : '欢迎回来' }}</h2>
      <p class="muted">{{ initializing ? '仅首次部署可用，需要主机生成的一次性初始化凭据。' : '使用管理员账号登录工作空间。' }}</p>
      <ErrorNotice :error="error || auth.restoreError" />
      <button v-if="auth.restoreError" class="text-button" type="button" @click="retryRestore">重试恢复会话</button>
      <form @submit.prevent="submit">
        <label>用户名<input v-model="username" name="username" autocomplete="username" pattern="[a-z0-9._-]+" maxlength="64" required autofocus data-field="/username" /><span class="hint">小写字母、数字、点、下划线或连字符。</span></label>
        <label>{{ initializing ? '设置管理员密码' : '密码' }}
          <input
            v-model="password"
            name="password"
            type="password"
            :autocomplete="initializing ? 'new-password' : 'current-password'"
            maxlength="1024" required data-field="/password"
          />
          <span v-if="initializing" class="hint">新密码至少 12 个 UTF-8 字节，不会自动去除空格。</span>
        </label>
        <label v-if="initializing">初始化凭据<input v-model="setupToken" name="setup-token" type="password" autocomplete="off" minlength="43" maxlength="43" required data-field="/setup_token" /></label>
        <button class="full-width" :disabled="busy">{{ busy ? '正在处理…' : initializing ? '创建管理员并进入' : '登录' }}</button>
      </form>
      <p class="small auth-switch"><RouterLink :to="initializing ? '/login' : '/setup'">{{ initializing ? '已有账号，返回登录' : '首次部署？初始化管理员' }}</RouterLink></p>
    </section>
  </main>
</template>
