<script setup lang="ts">
import { ref, onBeforeUnmount } from 'vue'
import { useAuthStore } from '../stores/auth'
import AppDialog from './AppDialog.vue'
import ErrorNotice from './ErrorNotice.vue'
const auth = useAuthStore()
const password = ref('')
const busy = ref(false)
const error = ref<unknown>(null)
onBeforeUnmount(() => { password.value = '' })
async function submit() {
  busy.value = true; error.value = null
  const value = password.value
  password.value = ''
  try { await auth.reauthenticate(value) } catch (failure) { error.value = failure } finally { busy.value = false }
}
</script>

<template>
  <AppDialog title="再次验证身份" @close="auth.finishReauth(false)">
    <p class="muted">查看节点秘密前，请输入管理员密码。验证有效期为 5 分钟。</p>
    <ErrorNotice :error="error" />
    <form @submit.prevent="submit">
      <label>管理员密码<input v-model="password" type="password" autocomplete="current-password" required autofocus maxlength="1024" /></label>
      <div class="actions"><button type="button" class="secondary" @click="auth.finishReauth(false)">取消</button><button :disabled="busy">{{ busy ? '正在验证…' : '验证身份' }}</button></div>
    </form>
  </AppDialog>
</template>
