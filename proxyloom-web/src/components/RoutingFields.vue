<script setup lang="ts">
import type { Schema } from '../api/client'
import type { NetworkOptions } from '../domain/network-draft'
import { moveRule, newRoutingRule } from '../domain/network-draft'
import TargetSelect from './TargetSelect.vue'
import MatchFields from './MatchFields.vue'
const draft = defineModel<Schema<'RoutingProfile'>>({ required: true })
defineProps<{ options: NetworkOptions }>()
</script>
<template>
  <fieldset><legend>路由行为</legend><div class="form-grid"><label>域名解析模式<select v-model="draft.domain_resolution_mode" data-field="/routing_profile/domain_resolution_mode"><option value="preserve_domain">保留域名，不为 IP 规则额外解析</option><option value="resolve_for_ip_rules">通过显式 DNS 配置解析后匹配 IP 规则</option></select></label><TargetSelect v-model="draft.final" label="最终动作" :options="options.targets" path="/routing_profile/final" /></div><p v-if="draft.domain_resolution_mode === 'resolve_for_ip_rules'" class="hint">发布时需关联显式 DNS 配置，并检查目标内核是否支持等价语义。sing-box 1.14.0 在此模式含启用的 IP 条件时会明确拒绝编译：其 DNS 失败行为无法保持后续规则匹配，不会自动改为保留域名。IP 条件也包括引用规则集中的 CIDR。</p></fieldset>
  <div class="section-heading"><div><h2>有序路由规则</h2><p class="hint">从上到下匹配，首条命中生效；未命中时执行最终动作。不同字段 AND，同字段多项 OR。</p></div><button type="button" class="secondary" :disabled="draft.rules.length >= 1000" @click="draft.rules.push(newRoutingRule())">添加路由规则</button></div>
  <fieldset v-for="(rule, index) in draft.rules" :key="index"><legend>路由规则 {{ index + 1 }}</legend><div class="section-heading"><label class="checkbox-label"><input v-model="rule.enabled" type="checkbox" />启用此规则</label><div class="actions"><button type="button" class="text-button" :aria-label="`上移路由规则 ${index + 1}`" :disabled="index === 0" @click="moveRule(draft.rules, index, -1)">上移</button><button type="button" class="text-button" :aria-label="`下移路由规则 ${index + 1}`" :disabled="index === draft.rules.length - 1" @click="moveRule(draft.rules, index, 1)">下移</button><button type="button" class="text-button" :aria-label="`移除路由规则 ${index + 1}`" @click="draft.rules.splice(index, 1)">移除</button></div></div><div class="form-grid orchestration-gap"><label>备注<input v-model="rule.comment" maxlength="500" :data-field="`/routing_profile/rules/${index}/comment`" /></label><TargetSelect v-model="rule.action" label="命中动作" :options="options.targets" :path="`/routing_profile/rules/${index}/action`" /></div><MatchFields v-model="rule.match" routing :rule-sets="options.ruleSets" :path="`/routing_profile/rules/${index}/match`" /></fieldset>
</template>
