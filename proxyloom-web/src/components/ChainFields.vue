<script setup lang="ts">
import { computed } from 'vue'
import type { ChainDraft, MemberOption } from '../domain/orchestration-form'
import { swapHops } from '../domain/orchestration-form'
const draft = defineModel<ChainDraft>({ required: true })
const props = defineProps<{ options: MemberOption[] }>()
const nodes = computed(() => props.options.filter(option => option.ref.kind === 'node'))
const name = (id: string) => nodes.value.find(option => option.ref.resource_id === id)?.name ?? (id ? `不可用节点 · ${id}` : '请选择节点')
</script>

<template>
  <fieldset><legend>有序双跳链路</legend>
    <div class="chain-flow" aria-label="链路顺序"><span>客户端</span><span aria-hidden="true">→</span><div><strong>第一跳</strong><span>{{ name(draft.hops[0]) }}</span></div><span aria-hidden="true">→</span><div><strong>最终出口</strong><span>{{ name(draft.hops[1]) }}</span></div></div>
    <div class="form-grid">
      <label v-for="(label, index) in ['第一跳节点', '最终出口节点']" :key="label">{{ label }}<select v-model="draft.hops[index]" required :data-field="`/hops/${index}/node_id`"><option value="" disabled>选择已启用节点</option><option v-if="draft.hops[index] && !nodes.some(option => option.ref.resource_id === draft.hops[index])" :value="draft.hops[index]" disabled>{{ name(draft.hops[index]) }}</option><option v-for="option in nodes" :key="option.ref.resource_id" :value="option.ref.resource_id" :disabled="option.ref.resource_id === draft.hops[1 - index]">{{ option.name }} · {{ option.ref.resource_id }}</option></select></label>
    </div>
    <div class="section-heading orchestration-gap"><p class="hint">仅选择两个不同的已启用节点。顺序以客户端视角为准，最后一跳为出口；不会修改原节点。</p><button type="button" class="secondary" :disabled="!draft.hops[0] || !draft.hops[1]" @click="swapHops(draft)">交换第一跳与出口</button></div>
    <p class="hint">失败策略：拒绝连接（fail_closed），不会自动直连。</p>
  </fieldset>
</template>
