<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import type { Schema } from '../api/client'
import { collection } from '../domain/subscription'
import { jobTypes, bytes } from '../domain/test-result'
import ErrorNotice from './ErrorNotice.vue'
const props = defineProps<{ subjects: Schema<'TestSubject'>[] }>()
const router = useRouter(), cores = ref<Schema<'CoreBuild'>[]>([]), targets = ref<Schema<'TestTarget'>[]>([])
const type = ref<Schema<'RunnerJobType'>>('connectivity'), core = ref(''), target = ref(''), seconds = ref(10), mebibytes = ref(20)
const loaded = ref(false), busy = ref(false), error = ref<unknown>(null)
const controller = new AbortController()
let key = '', submitted = ''
async function load() {
  busy.value = true; error.value = null
  try {
    const [c, t] = await Promise.all([collection<Schema<'CoreBuild'>>('/cores', controller.signal), collection<Schema<'TestTarget'>>('/test-targets', controller.signal)])
    cores.value = c.filter(item => item.enabled); targets.value = t.filter(item => item.enabled && item.safety_state === 'approved')
    core.value = cores.value.find(item => item.architecture === 'amd64')?.core_build_id ?? cores.value[0]?.core_build_id ?? ''
    target.value = targets.value.find(item => item.target.allowed_types.includes(type.value as 'connectivity' | 'download_throughput'))?.test_target_id ?? ''
    loaded.value = true
  } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false }
}
watch(type, () => { if (!targets.value.some(item => item.test_target_id === target.value && item.target.allowed_types.includes(type.value as 'connectivity' | 'download_throughput'))) target.value = '' })
async function submit() {
  if (!props.subjects.length || props.subjects.length > 100) return
  busy.value = true; error.value = null
  const body: Schema<'TestCreateRequest'> = { subjects: props.subjects, core_build_id: core.value, type: type.value, limits: { duration_ms: seconds.value * 1000, max_bytes: type.value === 'config_validate' ? 0 : type.value === 'connectivity' ? 3 * 65536 : Math.floor(mebibytes.value * 1048576) }, ...(type.value === 'config_validate' ? {} : { test_target_id: target.value }) }
  const encoded = JSON.stringify(body)
  if (!key || submitted !== encoded) { key = crypto.randomUUID(); submitted = encoded }
  try {
    const result = await api<Schema<'TestBatchResponse'>>('/tests', { method: 'POST', body, idempotencyKey: key, signal: controller.signal })
    key = ''; submitted = ''
    await router.push(`/jobs/${result.body.data.batch_id}`)
  } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false }
}
onBeforeUnmount(() => controller.abort())
</script>
<template>
  <section class="panel" aria-label="测试所选对象"><div class="section-heading"><h2>测试所选对象（{{ subjects.length }} / 100）</h2><button v-if="!loaded" type="button" class="secondary" :disabled="busy || !subjects.length || subjects.length > 100" @click="load">选择测试参数</button></div>
    <ErrorNotice :error="error" />
    <form v-if="loaded" class="form-grid" @submit.prevent="submit">
      <label>测试类型<select v-model="type"><option v-for="kind in (['config_validate', 'connectivity', 'download_throughput'] as const)" :key="kind" :value="kind">{{ jobTypes[kind] }}</option></select></label>
      <label>执行内核<select v-model="core" required><option value="" disabled>选择内核构建</option><option v-for="item in cores" :key="item.core_build_id" :value="item.core_build_id">{{ item.core_family }} {{ item.version }} · {{ item.architecture }}</option></select></label>
      <label v-if="type !== 'config_validate'">登记目标<select v-model="target" required><option value="" disabled>选择允许该测试的目标</option><option v-for="item in targets.filter(item => item.target.allowed_types.includes(type as 'connectivity' | 'download_throughput'))" :key="item.test_target_id" :value="item.test_target_id">{{ item.name }} · r{{ item.revision }}</option></select><RouterLink to="/test-targets">管理测试目标</RouterLink></label>
      <label>时间上限（秒）<input v-model.number="seconds" type="number" min="1" max="60" step="1" required /></label>
      <label v-if="type === 'download_throughput'">下载上限（MiB）<input v-model.number="mebibytes" type="number" min="0.001" max="20" step="0.001" required /></label>
      <p class="hint span-2">服务端会冻结对象、完整两跳依赖和有效限额。{{ type === 'connectivity' ? '三个小响应样本，中位数展示；单对象最多预留 ' + bytes(3 * 65536) + '。' : type === 'download_throughput' ? '单样本达到时间或字节上限即停止；不足 1 秒或 1 MiB 不展示吞吐结论。' : '离线配置校验不消耗网络测试预算。' }}额度不足时整批拒绝。</p>
      <div class="actions span-2"><button :disabled="busy || !subjects.length || subjects.length > 100">{{ busy ? '正在提交…' : '创建测试任务' }}</button></div>
    </form>
  </section>
</template>
