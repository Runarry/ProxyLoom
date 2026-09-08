<script setup lang="ts">
import type { SecretDraft } from '../domain/node-form'
const model = defineModel<SecretDraft>({ required: true })
defineProps<{ label: string; path: string; editing: boolean; allowEmpty?: boolean }>()
function changeMode() { model.value.value = '' }
</script>

<template>
  <div class="secret-field">
    <label>{{ label }}<select v-model="model.mode" :aria-label="`${label}操作`" @change="changeMode"><option v-if="model.canKeep" value="keep">保留已保存的值</option><option value="replace">输入新值</option><option v-if="editing" value="clear">明确清除（null）</option></select></label>
    <label v-if="model.mode === 'replace'"><span class="sr-only">{{ label }}新值</span><input v-model="model.value" :aria-label="`${label}新值`" type="password" autocomplete="new-password" :required="!allowEmpty" maxlength="4096" :data-field="path" /></label>
    <p v-else class="hint">{{ model.mode === 'keep' ? '已设置 · 保存时省略此秘密字段。' : '将发送 null。必填凭据不允许清除，服务端会返回字段错误。' }}</p>
  </div>
</template>
