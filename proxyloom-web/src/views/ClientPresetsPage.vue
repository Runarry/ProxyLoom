<script setup lang="ts">
import { ref, reactive, onMounted, onBeforeUnmount } from 'vue'
import { api, queryString, localTime } from '../api/client'
import type { Schema } from '../api/client'
import ErrorNotice from '../components/ErrorNotice.vue'
const filters = reactive({ core_family: '', platform: '' })
const applied = ref({ ...filters })
const presets = ref<Schema<'ClientPresetResource'>[]>([])
const cursors = ref(['']), page = ref(0), nextCursor = ref('')
const loading = ref(false), error = ref<unknown>(null)
let controller: AbortController | undefined
async function load() {
  controller?.abort(); const request = controller = new AbortController()
  loading.value = true; error.value = null
  try {
    const response = await api<Schema<'ClientPresetListResponse'>>(`/client-presets${queryString({ ...applied.value, limit: 50, cursor: cursors.value[page.value] })}`, { signal: request.signal })
    if (!request.signal.aborted) { presets.value = response.body.data; nextCursor.value = response.body.page.next_cursor ?? '' }
  } catch (failure) { if (!request.signal.aborted) error.value = failure }
  finally { if (!request.signal.aborted) loading.value = false }
}
function filter() { applied.value = { ...filters }; page.value = 0; cursors.value = ['']; void load() }
function movePage(delta: number) { if (delta > 0) cursors.value[page.value + 1] = nextCursor.value; page.value += delta; void load() }
onMounted(load)
onBeforeUnmount(() => controller?.abort())
</script>
<template>
  <main id="main" class="content"><div class="page-heading"><div><p class="eyebrow">编排 / 客户端</p><h1>客户端预设</h1><p class="muted">查看系统已审核预设的内核、平台和本机监听参数。</p></div><span class="badge neutral">系统预设 · 只读</span></div>
    <div class="notice info"><p>预设审核状态不代表当前配置已通过兼容性验证。内核加载验证、运行时行为与客户端导入分别记录；当前页面没有这些验证结果，均为未验证（unverified）。发布仍需冻结具体预设修订，并对全部启用目标执行真实内核校验。</p></div>
    <form class="panel filters" @submit.prevent="filter"><label>内核<select v-model="filters.core_family"><option value="">全部内核</option><option value="xray">Xray</option><option value="sing-box">sing-box</option><option value="mihomo">Mihomo</option></select></label><label>平台<select v-model="filters.platform"><option value="">全部平台</option><option v-for="platform in ['linux', 'windows', 'macos', 'android', 'ios']" :key="platform" :value="platform">{{ platform }}</option></select></label><button :disabled="loading">筛选</button></form>
    <ErrorNotice :error="error" /><div v-if="loading" class="empty-state" role="status">正在读取客户端预设…</div><div v-else-if="!presets.length" class="empty-state"><h2>{{ error ? '暂时无法读取预设' : '暂无符合条件的预设' }}</h2><button v-if="error" type="button" class="secondary" @click="load">重新读取</button></div>
    <div class="preset-grid"><section v-for="resource in presets" :key="resource.metadata.resource_id" class="panel"><div class="section-heading"><h2>{{ resource.metadata.name }}</h2><span class="badge neutral">r{{ resource.metadata.revision }}</span></div><dl class="detail-list"><dt>内核 / 平台</dt><dd>{{ resource.preset.core_family }} / {{ resource.preset.platform }}</dd><dt>输出格式</dt><dd>{{ resource.preset.format }}</dd><dt>导入方式</dt><dd>{{ resource.preset.import_method === 'file' ? '配置文件' : '订阅地址' }}</dd><dt>本机监听</dt><dd>{{ resource.preset.local_listener.protocol }} · {{ resource.preset.local_listener.listen }}:{{ resource.preset.local_listener.port }}</dd><dt>控制接口</dt><dd>{{ resource.preset.control_api.enabled ? `已启用 · ${resource.preset.control_api.listen}:${resource.preset.control_api.port}` : '已关闭' }}</dd><dt>DNS 模式</dt><dd>显式 DNS 配置</dd><dt>审核状态</dt><dd>已审核 · {{ localTime(resource.preset.reviewed_at) }}</dd><dt>资源状态</dt><dd>{{ resource.metadata.enabled ? '已启用' : '已停用' }}</dd><dt>稳定 ID</dt><dd class="mono">{{ resource.metadata.resource_id }}</dd></dl></section></div>
    <div class="pagination"><span>第 {{ page + 1 }} 页 · 本页 {{ presets.length }} 条</span><div class="actions"><button type="button" class="secondary" :disabled="!page || loading" @click="movePage(-1)">上一页</button><button type="button" class="secondary" :disabled="!nextCursor || loading" @click="movePage(1)">下一页</button></div></div>
  </main>
</template>
