<script setup lang="ts">
import { computed } from 'vue'
import type { Schema } from '../api/client'
import type { TargetOption } from '../domain/network-draft'
import { targetKey, targetLabel } from '../domain/network-draft'
const target = defineModel<Schema<'TargetRef'>>({ required: true })
const props = defineProps<{ label: string; options: TargetOption[]; path: string }>()
const selected = computed({ get: () => targetKey(target.value), set: (key: string) => {
  if (key === 'builtin:direct' || key === 'builtin:reject') target.value = { type: 'builtin', builtin: key === 'builtin:direct' ? 'direct' : 'reject' }
  else { const option = props.options.find(option => targetKey(option.ref) === key); if (option) target.value = { ...option.ref } }
} })
const unavailable = computed(() => target.value.type === 'resource_ref' && !props.options.some(option => targetKey(option.ref) === selected.value))
</script>
<template><label>{{ label }}<select v-model="selected" required :data-field="path"><option value="builtin:reject">拒绝连接</option><option value="builtin:direct">直连</option><option v-if="unavailable" :value="selected" disabled>不可用资源 · {{ targetLabel(target) }}</option><optgroup v-for="(group, kind) in { node: '节点', chain: '链路', policy_group: '策略组' }" :key="kind" :label="group"><option v-for="option in options.filter(option => option.ref.kind === kind)" :key="targetKey(option.ref)" :value="targetKey(option.ref)">{{ option.name }} · {{ option.ref.resource_id }}</option></optgroup></select></label></template>
