<script setup lang="ts">
import { ref, reactive, onMounted, onBeforeUnmount } from 'vue'
import { api, revisionTag } from '../api/client'
import type { Schema } from '../api/client'
import { collection } from '../domain/subscription'
import { DraftError } from '../domain/node-form'
import ErrorNotice from '../components/ErrorNotice.vue'
const rows = ref<Schema<'TestTarget'>[]>([]), editing = ref<Schema<'TestTarget'> | null>(null), busy = ref(false), error = ref<unknown>(null), notice = ref('')
const defaults = () => ({ name: '', url: '', enabled: true, connectivity: true, download: false, seconds: 10, mebibytes: 20, statuses: '200', hash: '', egress: false, responseBytes: 65536, permission: 'self_owned' as 'self_owned' | 'explicitly_authorized' })
const form = reactive(defaults()), controller = new AbortController(); let key = '', submitted = ''
async function load() { rows.value = await collection<Schema<'TestTarget'>>('/test-targets', controller.signal) }
function edit(item: Schema<'TestTarget'>) {
  editing.value = item; notice.value = ''; error.value = null
  Object.assign(form, { name: item.name, url: item.target.url, enabled: item.enabled, connectivity: item.target.allowed_types.includes('connectivity'), download: item.target.allowed_types.includes('download_throughput'), seconds: item.target.limits.duration_ms / 1000, mebibytes: item.target.limits.max_bytes / 1048576, statuses: item.target.expected_response.status_codes.join(','), hash: item.target.expected_response.body_sha256 ?? '', egress: item.target.expected_response.egress_ip_response, responseBytes: item.target.expected_response.max_response_bytes, permission: item.target.permission_basis })
  document.getElementById('target-name')?.focus()
}
async function save() {
  busy.value = true; error.value = null; notice.value = ''
  try {
    const types: ('connectivity' | 'download_throughput')[] = []
    if (form.connectivity) types.push('connectivity'); if (form.download) types.push('download_throughput')
    const codes = form.statuses.split(',').map(value => Number(value.trim()))
    if (!types.length || !codes.length || codes.some(value => !Number.isInteger(value) || value < 100 || value > 599)) throw new DraftError('请至少选择一种测试类型，并填写逗号分隔的 HTTP 状态码。')
    const body: Schema<'TestTargetCreateRequest'> = { name: form.name, enabled: form.enabled, target: { url: form.url, allowed_types: types, limits: { duration_ms: form.seconds * 1000, max_bytes: Math.floor(form.mebibytes * 1048576) }, expected_response: { status_codes: codes, max_response_bytes: editing.value ? form.responseBytes : form.download ? Math.floor(form.mebibytes * 1048576) : 65536, egress_ip_response: form.egress, ...(form.hash ? { body_sha256: form.hash } : {}) }, permission_basis: form.permission, redirect_policy: 'deny', verify_certificate: true, compression: 'disabled' } }
    const encoded = JSON.stringify(body)
    if (!key || submitted !== encoded) { key = crypto.randomUUID(); submitted = encoded }
    await api(editing.value ? `/test-targets/${editing.value.test_target_id}` : '/test-targets', { method: editing.value ? 'PATCH' : 'POST', body, ...(editing.value ? { etag: revisionTag(editing.value.revision) } : { idempotencyKey: key }), signal: controller.signal })
    key = ''; submitted = ''; editing.value = null; Object.assign(form, defaults()); await load(); notice.value = '测试目标已保存。实际测试仍会校验证书、状态与响应特征。'
  } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false }
}
async function remove(item: Schema<'TestTarget'>) {
  busy.value = true; error.value = null
  try { await api(`/test-targets/${item.test_target_id}`, { method: 'DELETE', etag: revisionTag(item.revision), signal: controller.signal }); if (editing.value?.test_target_id === item.test_target_id) { editing.value = null; Object.assign(form, defaults()) }; await load() }
  catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false }
}
onMounted(() => load().catch(failure => { error.value = failure })); onBeforeUnmount(() => controller.abort())
</script>
<template>
  <main id="main" class="content"><div class="page-heading"><div><p class="eyebrow">工作空间 / 测试目标</p><h1>受控测试目标</h1><p>仅登记自己拥有或已获明确授权的 HTTP(S) 服务。HTTPS 始终验证证书。</p></div><RouterLink class="button secondary" to="/tests">创建测试</RouterLink></div><ErrorNotice :error="error" /><p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <form class="panel form-grid" @submit.prevent="save"><h2 class="span-2">{{ editing ? `编辑目标 · r${editing.revision}` : '登记目标' }}</h2><label>名称<input id="target-name" v-model="form.name" maxlength="256" required /></label><label>HTTP(S) URL<input v-model="form.url" type="url" maxlength="4096" pattern="https?://.+" required placeholder="https://controlled.example/test" /></label><fieldset><legend>允许测试</legend><label class="checkbox"><input v-model="form.connectivity" type="checkbox" />连通性</label><label class="checkbox"><input v-model="form.download" type="checkbox" />下载测速</label></fieldset><label>授权依据<select v-model="form.permission"><option value="self_owned">自有服务</option><option value="explicitly_authorized">已获明确授权</option></select></label><label>预期 HTTP 状态码<input v-model="form.statuses" required placeholder="200,204" /></label><label>精确响应 SHA-256（可选）<input v-model="form.hash" pattern="[0-9a-f]{64}" maxlength="64" /></label><label>持续时间上限（秒）<input v-model.number="form.seconds" type="number" min="1" max="60" step="1" required /></label><label>读取字节上限（MiB）<input v-model.number="form.mebibytes" type="number" min="0.001" max="1024" step="0.001" required /></label><label class="checkbox"><input v-model="form.egress" type="checkbox" />响应为纯文本 IP 地址（最多 64 字节）</label><label class="checkbox"><input v-model="form.enabled" type="checkbox" />启用目标</label><p class="hint span-2">禁止跳转与压缩。登记时检查全部解析地址；执行前重新冻结批准地址。停用或删除会阻止新任务，并取消仍在执行的相关测试。</p><div class="actions span-2"><button :disabled="busy">{{ busy ? '处理中…' : '保存目标' }}</button><button v-if="editing" type="button" class="secondary" :disabled="busy" @click="editing = null; Object.assign(form, defaults())">返回新增</button></div></form>
    <section class="panel table-panel" aria-label="已登记目标"><div class="table-wrap"><table><thead><tr><th>目标</th><th>修订 / 状态</th><th>类型</th><th>操作</th></tr></thead><tbody><tr v-for="item in rows" :key="item.test_target_id"><td>{{ item.name }}<small>{{ item.target.url }}</small></td><td>r{{ item.revision }} · {{ item.enabled ? '已启用' : '已停用' }}</td><td>{{ item.target.allowed_types.map(type => type === 'connectivity' ? '连通性' : '下载测速').join('、') }}</td><td><button class="text-button" :disabled="busy" @click="edit(item)">编辑</button><button class="text-button danger" :disabled="busy" @click="remove(item)">删除</button></td></tr></tbody></table></div><p v-if="!rows.length" class="empty-state">尚未登记目标。</p></section>
  </main>
</template>
