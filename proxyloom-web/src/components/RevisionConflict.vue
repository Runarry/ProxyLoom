<script setup lang="ts">
import type { Schema } from '../api/client'
type Resource = { metadata: Schema<'ResourceMetadata'> }
defineProps<{ baseline: Resource | null; latest: Resource | null; busy: boolean }>()
const compared = defineModel<boolean>({ required: true })
defineEmits<{ compare: []; adopt: [keep: boolean] }>()
</script>
<template><section class="panel conflict-panel"><h2>修订冲突 · 草稿已保留</h2><p>读取最新版本并比较后，可继续保存当前草稿。</p><button type="button" class="secondary" :disabled="busy" @click="$emit('compare')">加载最新版本进行比较</button><template v-if="latest"><div class="comparison"><div><h3>编辑开始时 · r{{ baseline?.metadata.revision }}</h3><pre>{{ JSON.stringify(baseline, null, 2) }}</pre></div><div><h3>服务器最新 · r{{ latest.metadata.revision }}</h3><pre>{{ JSON.stringify(latest, null, 2) }}</pre></div></div><label class="checkbox-label"><input v-model="compared" type="checkbox" />已比较差异，确认基于最新修订继续编辑</label><div class="actions"><button type="button" class="secondary" :disabled="busy" @click="$emit('adopt', false)">放弃草稿，采用最新版本</button><button type="button" :disabled="busy || !compared" @click="$emit('adopt', true)">保留草稿，采用最新修订号</button></div></template></section></template>
