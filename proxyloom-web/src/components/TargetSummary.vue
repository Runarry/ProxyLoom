<script setup lang="ts">
import type { Schema } from '../api/client'
import { targetLabel } from '../domain/network-draft'
defineProps<{ target: Schema<'TargetRef'> }>()
const paths = { node: '/nodes', chain: '/chains', policy_group: '/policy-groups' }
</script>
<template><span v-if="target.type === 'builtin'">{{ targetLabel(target) }}</span><RouterLink v-else class="reference-link" :to="`${paths[target.kind]}/${target.resource_id}`">{{ targetLabel(target) }}</RouterLink></template>
