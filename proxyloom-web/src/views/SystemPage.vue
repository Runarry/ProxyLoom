<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { api, queryString, localTime } from '../api/client'
import type { Schema } from '../api/client'
import ErrorNotice from '../components/ErrorNotice.vue'
const settings = ref<Schema<'SystemSettings'>>(), original = ref(''), etag = ref(''), error = ref<unknown>(null), auditError = ref<unknown>(null), busy = ref(false), saved = ref(false)
const events = ref<Schema<'AuditEvent'>[]>([]), action = ref(''), appliedAction = ref(''), cursors = ref(['']), page = ref(0), next = ref(''), auditBusy = ref(false)
const controller = new AbortController(); let auditController: AbortController | undefined
const dirty = computed(() => !!settings.value && JSON.stringify(settings.value) !== original.value)
type Group = 'quota' | 'retention' | 'catalog_limits'
const groups: { key: Group; label: string; fields: { key: string; label: string; min: number; max: number }[] }[] = [
  { key: 'quota', label: '预算与执行限额', fields: [
    { key: 'daily_download_bytes', label: 'UTC 日预算（字节）', min: 1, max: Number.MAX_SAFE_INTEGER }, { key: 'connectivity_concurrency', label: '连通性并发', min: 1, max: 4 }, { key: 'throughput_concurrency', label: '吞吐并发', min: 1, max: 1 }, { key: 'max_test_duration_ms', label: '单任务测试时限（毫秒）', min: 1000, max: 60000 }, { key: 'max_test_bytes', label: '单任务读取上限（字节）', min: 1, max: 1073741824 }, { key: 'minimum_throughput_sample_bytes', label: '吞吐样本最低字节数', min: 1, max: 1073741824 },
  ] }, { key: 'retention', label: '历史保留', fields: [
    { key: 'job_event_days', label: '任务事件与脱敏日志（天）', min: 1, max: 365 }, { key: 'test_result_days', label: '测试结果（天）', min: 1, max: 3650 }, { key: 'audit_days', label: '审计（天）', min: 1, max: 3650 }, { key: 'idempotency_hours', label: '幂等回执（小时）', min: 1, max: 168 }, { key: 'import_days', label: '导入预览（天）', min: 1, max: 365 }, { key: 'publication_days', label: '发布历史（天）', min: 1, max: 3650 }, { key: 'publication_count', label: '每个订阅保留发布数', min: 1, max: 1000 },
  ] }, { key: 'catalog_limits', label: '资源与编译限制', fields: [
    { key: 'max_nodes', label: '节点总数', min: 1, max: 1000000 }, { key: 'max_import_bytes', label: '导入大小（字节）', min: 1, max: 10485760 }, { key: 'max_import_items', label: '单次导入条数', min: 1, max: 5000 }, { key: 'max_dependency_resources', label: '编译依赖资源数', min: 1, max: 10000 }, { key: 'max_targets_per_subscription', label: '订阅目标数', min: 1, max: 32 }, { key: 'max_rules', label: '规则数', min: 1, max: 20000 }, { key: 'max_outbounds', label: '展开出站数', min: 1, max: 2000 },
  ] },
]
const actions: Record<Schema<'AuditAction'>, string> = { setup: '初始化', login: '登录', logout: '退出', reauth: '再次认证', create: '创建', update: '修改', delete: '删除', reveal: '查看凭证', source_refresh: '刷新来源', import_commit: '导入确认', compile: '编译', publish: '发布', rollback: '回滚', token_issue: '签发令牌', token_revoke: '撤销令牌', test_create: '创建测试', job_cancel: '取消任务', core_disable: '停用构建', settings_update: '修改设置', secret_rewrap: '密钥轮换', export: '导出', cleanup: '清理历史', backup: '备份', restore: '恢复', password_reset: '重置密码' }
function numeric(group: Group) { return settings.value![group] as Record<string, number> }
function cidrs() { return settings.value!.private_proxy_cidrs.join('\n') }
function setCIDRs(value: string) { settings.value!.private_proxy_cidrs = value.split(/[\n,]/).map(item => item.trim()).filter(Boolean) }
async function load() {
  if (dirty.value && !window.confirm('重新读取会丢弃当前未保存设置，是否继续？')) return
  busy.value = true; error.value = null; saved.value = false
  try { const r = await api<Schema<'SettingsResponse'>>('/system/settings', { signal: controller.signal }); settings.value = r.body.data; original.value = JSON.stringify(r.body.data); etag.value = r.etag ?? '' }
  catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false }
}
async function save() {
  if (!settings.value) return
  busy.value = true; error.value = null; saved.value = false
  try {
    const { revision: _revision, ...body } = settings.value
    const r = await api<Schema<'SettingsResponse'>>('/system/settings', { method: 'PATCH', body, etag: etag.value })
    settings.value = r.body.data; original.value = JSON.stringify(r.body.data); etag.value = r.etag ?? ''; saved.value = true; filterAudit()
  } catch (failure) { error.value = failure } finally { busy.value = false }
}
async function loadAudit() {
  auditController?.abort(); const request = auditController = new AbortController(); auditBusy.value = true; auditError.value = null
  try { const r = await api<Schema<'AuditEventListResponse'>>(`/audit-events${queryString({ action: appliedAction.value, cursor: cursors.value[page.value], limit: 50 })}`, { signal: request.signal }); events.value = r.body.data; next.value = r.body.page.next_cursor ?? '' }
  catch (failure) { if (!request.signal.aborted) auditError.value = failure } finally { if (!request.signal.aborted) auditBusy.value = false }
}
function filterAudit() { appliedAction.value = action.value; cursors.value = ['']; page.value = 0; void loadAudit() }
function move(delta: number) { if (delta > 0) cursors.value[page.value + 1] = next.value; page.value += delta; void loadAudit() }
function beforeUnload(e: BeforeUnloadEvent) { if (dirty.value) e.preventDefault() }
onBeforeRouteLeave(() => !dirty.value || window.confirm('设置尚未保存，是否离开此页？'))
onMounted(() => { void load(); void loadAudit(); window.addEventListener('beforeunload', beforeUnload) })
onBeforeUnmount(() => { controller.abort(); auditController?.abort(); window.removeEventListener('beforeunload', beforeUnload) })
</script>
<template>
  <main id="main" class="content"><div class="page-heading"><div><p class="eyebrow">系统 / 运维</p><h1>系统设置与审计</h1><p>设置按修订保存，测试任务与编译批次使用创建时的有效限额。</p></div><button class="secondary" :disabled="busy" @click="load">重新读取设置</button></div>
    <ErrorNotice :error="error" /><p v-if="saved" class="notice success" role="status">设置已保存 · r{{ settings?.revision }}</p>
    <form v-if="settings" @submit.prevent="save"><fieldset v-for="group in groups" :key="group.key" class="panel" :disabled="busy"><legend>{{ group.label }}</legend><div class="form-grid"><label v-for="field in group.fields" :key="field.key">{{ field.label }}<input v-model.number="numeric(group.key)[field.key]" type="number" required step="1" :min="field.min" :max="field.max" /></label></div></fieldset><fieldset class="panel" :disabled="busy"><legend>私网代理节点白名单</legend><label>允许 CIDR（每行一个）<textarea :value="cidrs()" rows="4" maxlength="303" placeholder="192.168.5.0/24" @input="setCIDRs(($event.target as HTMLTextAreaElement).value)" /></label><p>仅允许在线测试连接白名单内的第一跳或链路节点。登记测试目标仍必须是公网地址。</p></fieldset><section class="panel"><label class="checkbox-label"><input v-model="settings.cleanup_paused" type="checkbox" :disabled="busy" />暂停历史清理</label><p>清理保护活动发布、必要修订和未结束任务。缩短保留周期会在后续清理中生效。</p><div class="actions"><button :disabled="busy || !dirty">保存系统设置</button><span>r{{ settings.revision }} · {{ dirty ? '有未保存修改' : '已保存' }}</span></div></section></form>
    <section class="panel"><h2>审计事件</h2><form class="filters" @submit.prevent="filterAudit"><label>审计操作<select v-model="action"><option value="">全部操作</option><option v-for="(label, value) in actions" :key="value" :value="value">{{ label }}</option></select></label><button class="secondary" :disabled="auditBusy">筛选审计</button></form><ErrorNotice :error="auditError" /><div class="table-wrap"><table><thead><tr><th>时间</th><th>操作 / 结果</th><th>关联对象</th><th>修改字段</th><th>请求编号</th></tr></thead><tbody><tr v-for="event in events" :key="event.event_id"><td>{{ localTime(event.created_at) }}</td><td>{{ actions[event.action] }} · {{ event.outcome === 'success' ? '成功' : '失败' }}<small>{{ { administrator: '管理员', runner: 'Runner', system: '系统' }[event.actor_type] }}</small></td><td class="mono">{{ event.resource_id ?? '—' }}</td><td>{{ event.changed_fields.join('、') || '—' }}</td><td class="mono">{{ event.request_id }}</td></tr></tbody></table></div><p v-if="!events.length" role="status">{{ auditBusy ? '正在读取审计…' : '没有符合条件的事件。' }}</p><div class="pagination"><span>第 {{ page + 1 }} 页</span><div class="actions"><button class="secondary" :disabled="!page || auditBusy" @click="move(-1)">上一页</button><button class="secondary" :disabled="!next || auditBusy" @click="move(1)">下一页</button></div></div></section>
  </main>
</template>
