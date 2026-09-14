<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { api, revisionTag, localTime } from '../api/client'
import type { Schema } from '../api/client'
import { collection } from '../domain/subscription'
import ErrorNotice from '../components/ErrorNotice.vue'
const cores = ref<Schema<'CoreBuild'>[]>([]), family = ref(''), architecture = ref(''), selected = ref<Schema<'CoreBuild'>>(), reason = ref('')
const error = ref<unknown>(null), busy = ref(false), controller = new AbortController()
const filtered = computed(() => cores.value.filter(item => (!family.value || item.core_family === family.value) && (!architecture.value || item.architecture === architecture.value)))
const status = { verified: '已验证', unverified: '未验证', unsupported: '不支持' }
async function load() { busy.value = true; error.value = null; try { cores.value = await collection<Schema<'CoreBuild'>>('/cores', controller.signal) } catch (failure) { if (!controller.signal.aborted) error.value = failure } finally { busy.value = false } }
async function disable() {
  if (!selected.value) return
  busy.value = true; error.value = null
  try {
    await api<Schema<'CoreResponse'>>(`/cores/${selected.value.core_build_id}/disable`, { method: 'POST', etag: revisionTag(selected.value.revision), body: { reason: reason.value }, idempotencyKey: crypto.randomUUID() })
    selected.value = undefined; reason.value = ''; await load()
  } catch (failure) { error.value = failure } finally { busy.value = false }
}
onMounted(load); onBeforeUnmount(() => controller.abort())
</script>
<template>
  <main id="main" class="content"><div class="page-heading"><div><p class="eyebrow">系统 / 内核</p><h1>内核构建</h1><p>构建来自锁定镜像清单，具体协议与客户端组合另有兼容性证据。</p></div><button class="secondary" :disabled="busy" @click="load">刷新构建</button></div>
    <ErrorNotice :error="error" /><form class="panel filters" @submit.prevent><label>内核<select v-model="family"><option value="">全部</option><option v-for="value in ['xray', 'sing-box', 'mihomo']" :key="value">{{ value }}</option></select></label><label>架构<select v-model="architecture"><option value="">全部</option><option>amd64</option><option>arm64</option></select></label></form>
    <section v-if="selected" class="panel"><h2>停用 {{ selected.core_family }} {{ selected.version }} / {{ selected.architecture }}</h2><p>停用将阻止该构建的新任务、发布和订阅下载。已下载配置不会自动撤回。</p><form class="form-grid" @submit.prevent="disable"><label>停用原因<input v-model="reason" required maxlength="256" /></label><div class="actions"><button class="danger" :disabled="busy">确认停用构建</button><button type="button" class="secondary" :disabled="busy" @click="selected = undefined">保留构建</button></div></form></section>
    <div class="preset-grid"><section v-for="core in filtered" :key="core.core_build_id" class="panel"><h2>{{ core.core_family }} {{ core.version }}</h2><dl class="detail-list"><dt>平台 / 架构</dt><dd>{{ core.platform }} / {{ core.architecture }}</dd><dt>状态</dt><dd>{{ core.enabled ? '可用' : '已停用' }} · {{ status[core.capability_status] }}</dd><dt>适配器</dt><dd>{{ core.adapter_version }}</dd><dt>构建 SHA-256</dt><dd class="mono">{{ core.build_sha256 }}</dd><dt>登记时间</dt><dd>{{ localTime(core.registered_at) }}</dd><template v-if="!core.enabled"><dt>停用原因</dt><dd>{{ core.disable_reason }}</dd></template></dl><button class="secondary" :disabled="busy || !core.enabled" @click="selected = core; reason = ''">停用此构建</button></section></div><p v-if="!filtered.length" role="status">{{ busy ? '正在读取构建…' : '没有符合条件的构建。' }}</p>
  </main>
</template>
