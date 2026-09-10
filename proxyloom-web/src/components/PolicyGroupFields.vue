<script setup lang="ts">
import { computed, ref } from 'vue'
import type { PolicyDraft, MemberOption } from '../domain/orchestration-form'
import { memberKey, removeMember, moveMember, enableHealthCheck, strategyLabels, strategyDescriptions } from '../domain/orchestration-form'
const draft = defineModel<PolicyDraft>({ required: true })
const props = defineProps<{ options: MemberOption[] }>()
const candidate = ref('')
const available = computed(() => props.options.filter(option => !draft.value.members.some(member => member.resource_id === option.ref.resource_id)))
const label = (key: string) => {
  const option = props.options.find(item => memberKey(item.ref) === key)
  return option ? `${option.ref.kind === 'node' ? '节点' : '链路'} · ${option.name} · ${option.ref.resource_id}` : `不可用成员 · ${key}`
}
function add() {
  const option = available.value.find(item => memberKey(item.ref) === candidate.value)
  if (!option || draft.value.members.length >= 200) return
  draft.value.members.push({ ...option.ref }); candidate.value = ''
}
</script>

<template>
  <fieldset><legend>选择策略</legend><label>策略<select v-model="draft.strategy" data-field="/policy_group/strategy"><option v-for="(label, value) in strategyLabels" :key="value" :value="value">{{ label }}</option></select></label><p class="hint">{{ strategyDescriptions[draft.strategy] }}</p></fieldset>
  <fieldset><legend>有序成员 · {{ draft.members.length }} / 200</legend>
    <div class="inline-form"><label>添加节点或链路<select v-model="candidate" data-field="/policy_group/members"><option value="" disabled>选择已启用的候选资源</option><optgroup v-for="kind in (['node', 'chain'] as const)" :key="kind" :label="kind === 'node' ? '节点' : '链路'"><option v-for="option in available.filter(item => item.ref.kind === kind)" :key="memberKey(option.ref)" :value="memberKey(option.ref)">{{ option.name }} · {{ option.ref.resource_id }}</option></optgroup></select></label><button type="button" class="secondary" :disabled="!candidate || draft.members.length >= 200" @click="add">添加成员</button></div>
    <ol class="member-list"><li v-for="(member, index) in draft.members" :key="memberKey(member)"><span>{{ label(memberKey(member)) }}</span><div class="actions"><button type="button" class="text-button" :disabled="index === 0" :aria-label="`上移成员 ${index + 1}`" @click="moveMember(draft, index, -1)">上移</button><button type="button" class="text-button" :disabled="index === draft.members.length - 1" :aria-label="`下移成员 ${index + 1}`" @click="moveMember(draft, index, 1)">下移</button><button type="button" class="text-button" :aria-label="`移除成员 ${index + 1}`" @click="removeMember(draft, index)">移除</button></div></li></ol>
    <p v-if="!draft.members.length" class="hint">请添加至少一个节点或链路成员。</p>
    <label>默认成员<select v-model="draft.defaultMember" required data-field="/policy_group/default_member"><option value="" disabled>从已选成员中选择</option><option v-for="member in draft.members" :key="memberKey(member)" :value="memberKey(member)">{{ label(memberKey(member)) }}</option></select></label>
    <p class="hint">默认成员必须属于此策略组。移除默认成员后需重新选择；策略组不嵌套其他组，也不插入直连成员。</p>
  </fieldset>
  <fieldset><legend>客户端健康检查</legend><label class="checkbox-label"><input v-model="draft.health.enabled" type="checkbox" data-field="/policy_group/health_check/enabled" @change="enableHealthCheck(draft)" />启用客户端健康检查</label>
    <div v-if="draft.health.enabled" class="form-grid"><label class="span-2">检查地址（HTTPS）<input v-model="draft.health.url" type="url" required maxlength="2048" placeholder="https://example.com/check" data-field="/policy_group/health_check/url" /></label><label>检查间隔（毫秒）<input v-model.number="draft.health.interval_ms" type="number" required min="1000" max="86400000" step="1" data-field="/policy_group/health_check/interval_ms" /></label><label>检查超时（毫秒）<input v-model.number="draft.health.timeout_ms" type="number" required min="100" max="60000" step="1" data-field="/policy_group/health_check/timeout_ms" /></label><label>延迟容差（毫秒）<input v-model.number="draft.health.tolerance_ms" type="number" required min="0" max="60000" step="1" data-field="/policy_group/health_check/tolerance_ms" /></label></div>
    <p class="hint">这些字段用于客户端运行时观测，与平台后台测试分开。目标不支持该行为时会阻止发布。不可用策略：拒绝连接（fail_closed）。</p>
  </fieldset>
</template>
