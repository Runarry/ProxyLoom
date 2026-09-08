<script setup lang="ts">
import { APIError, errorMessage } from '../api/client'
defineProps<{ error: unknown }>()
function focusField(path: string) {
  const control = document.querySelector<HTMLElement>(`[data-field="${CSS.escape(path)}"]`)
    ?? document.querySelector<HTMLElement>(`[data-field="${CSS.escape(path.replace(/^\/node\//, '/'))}"]`)
  control?.focus()
  control?.scrollIntoView({ block: 'center', behavior: 'smooth' })
}
</script>

<template>
  <div v-if="error" class="notice error" role="alert">
    <strong>{{ errorMessage(error) }}</strong>
    <template v-if="error instanceof APIError">
      <p class="small">{{ error.code }} · HTTP {{ error.status || '网络' }}<span v-if="error.requestId"> · 请求编号 {{ error.requestId }}</span></p>
      <p v-if="error.retryAfter" class="small">重试等待：{{ error.retryAfter }}</p>
      <ul v-if="error.details.length" class="field-errors">
        <li v-for="(detail, index) in error.details" :key="index">
          <button v-if="detail.field_path" class="text-button" type="button" @click="focusField(detail.field_path)">检查字段 {{ detail.field_path }}</button>
          <span v-else>{{ detail.resource_id }}</span>
        </li>
      </ul>
    </template>
  </div>
</template>
