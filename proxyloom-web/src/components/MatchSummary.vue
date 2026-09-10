<script setup lang="ts">
import type { Schema } from '../api/client'
defineProps<{ match: Schema<'RouteMatch'> }>()
</script>
<template><dl class="detail-list match-summary"><template v-for="(label, key) in { domain_exact: '精确域名', domain_suffix: '域名后缀', ip_cidrs: 'IP 网段', network: '传输协议' }" :key="key"><template v-if="match[key]?.length"><dt>{{ label }}</dt><dd>{{ match[key]?.join('、') }}</dd></template></template><template v-if="match.destination_ports?.length"><dt>目标端口</dt><dd>{{ match.destination_ports.map(range => range.from === range.to ? `${range.from}` : `${range.from}–${range.to}`).join('、') }}</dd></template><template v-if="match.rule_set_ids?.length"><dt>规则集</dt><dd><RouterLink v-for="id in match.rule_set_ids" :key="id" class="reference-link" :to="`/rule-sets/${id}`">{{ id }}</RouterLink></dd></template></dl></template>
