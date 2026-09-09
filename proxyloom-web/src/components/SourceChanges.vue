<script setup lang="ts">
import type { Schema } from '../api/client'
defineProps<{ title: string; changes: Schema<'SourceFieldChange'>[] }>()
function display(value: unknown) { return value === undefined ? '未设置' : JSON.stringify(value, null, 2) }
</script>

<template>
  <details class="source-changes"><summary>{{ title }} · {{ changes.length }} 个字段</summary><p v-if="!changes.length" class="hint">没有字段变化。</p><dl v-else><template v-for="change in changes" :key="change.field_path"><dt><code>{{ change.field_path }}</code></dt><dd v-if="change.secret_changed" class="hint">秘密已变化</dd><dd v-else class="field-change"><span>之前</span><pre>{{ display(change.before) }}</pre><span>之后</span><pre>{{ display(change.after) }}</pre></dd></template></dl></details>
</template>
