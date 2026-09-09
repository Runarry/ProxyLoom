<script setup lang="ts">
import { computed } from 'vue'
import { changeProtocol, resetAuthentication, resetSecurity, protocols } from '../domain/node-form'
import type { NodeDraft, Protocol } from '../domain/node-form'
import SecretField from './SecretField.vue'
const draft = defineModel<NodeDraft>({ required: true })
defineProps<{ editing: boolean; busy: boolean; blocked?: boolean; bound?: boolean }>()
const emit = defineEmits<{ submit: [] }>()
const proxyAuth = computed(() => draft.value.protocol === 'socks5' || draft.value.protocol === 'http')
const securityModes = computed(() => draft.value.protocol === 'shadowsocks' ? ['none'] : draft.value.protocol === 'trojan' ? ['tls'] : draft.value.protocol === 'vless' ? ['none', 'tls', 'reality'] : ['none', 'tls'])
const websocketAllowed = computed(() => ['vmess', 'vless', 'trojan'].includes(draft.value.protocol) && draft.value.security !== 'reality' && !draft.value.vision)
function protocolChanged(event: Event) { draft.value = changeProtocol(draft.value, (event.target as HTMLSelectElement).value as Protocol) }
function transportChanged() { draft.value.wsPath = '/'; draft.value.wsHost = ''; draft.value.vision = false }
</script>

<template>
  <form class="node-form" @submit.prevent="emit('submit')">
    <fieldset :disabled="busy || blocked">
      <legend>基本信息</legend>
      <div class="form-grid"><label class="span-2">节点名称<input v-model="draft.name" required maxlength="256" data-field="/name" autofocus /></label><label>协议<select :value="draft.protocol" :disabled="bound" data-field="/protocol" @change="protocolChanged"><option v-for="protocol in protocols" :key="protocol.value" :value="protocol.value">{{ protocol.label }}</option></select><span v-if="bound" class="hint">绑定来源的节点不能覆盖协议。</span></label><label>标签<input v-model="draft.tags" data-field="/tags" placeholder="例如：香港, 常用" /><span class="hint">使用逗号分隔，每个标签最多 64 个字符。</span></label><label>服务器地址<input v-model="draft.host" required maxlength="253" data-field="/endpoint/host" placeholder="proxy.example.com" autocapitalize="none" spellcheck="false" /><span class="hint">小写域名或不带方括号的 IP 地址。</span></label><label>端口<input v-model.number="draft.port" type="number" required min="1" max="65535" step="1" data-field="/endpoint/port" /></label></div>
      <label class="checkbox-label"><input v-model="draft.enabled" type="checkbox" />启用节点</label>
    </fieldset>
    <fieldset :disabled="busy || blocked">
      <legend>认证</legend>
      <p v-if="editing" class="hint">秘密默认保留；切换协议或认证类型后，需要重新输入对应凭据。</p>
      <div class="form-grid">
        <label v-if="draft.protocol === 'shadowsocks'">加密方式<select v-model="draft.method" data-field="/auth/method"><option>aes-128-gcm</option><option>aes-256-gcm</option><option>chacha20-ietf-poly1305</option></select></label>
        <label v-if="draft.protocol === 'vmess'">VMess 加密<select v-model="draft.cipher" data-field="/auth/cipher"><option>auto</option><option>aes-128-gcm</option><option>chacha20-poly1305</option><option>none</option><option>zero</option></select></label>
        <label v-if="proxyAuth">认证方式<select v-model="draft.authenticated" @change="resetAuthentication(draft)"><option :value="false">无认证</option><option :value="true">用户名和密码</option></select></label>
        <SecretField v-if="draft.protocol === 'vmess' || draft.protocol === 'vless'" v-model="draft.uuid" label="UUID" path="/auth/uuid" :editing="editing" />
        <SecretField v-if="proxyAuth && draft.authenticated" v-model="draft.username" label="认证用户名" path="/auth/username" :editing="editing" />
        <SecretField v-if="draft.protocol === 'shadowsocks' || draft.protocol === 'trojan' || (proxyAuth && draft.authenticated)" v-model="draft.password" label="节点密码" path="/auth/password" :editing="editing" />
      </div>
    </fieldset>
    <fieldset :disabled="busy || blocked">
      <legend>传输与安全</legend>
      <div class="form-grid">
        <label>安全模式<select v-model="draft.security" data-field="/security/mode" @change="resetSecurity(draft)"><option v-for="mode in securityModes" :key="mode" :value="mode">{{ mode === 'none' ? '无 TLS' : mode === 'tls' ? 'TLS' : 'REALITY' }}</option></select></label>
        <label>传输<select v-model="draft.transport" data-field="/transport/kind" @change="transportChanged"><option value="native_tcp">原生 TCP</option><option v-if="websocketAllowed" value="websocket">WebSocket</option></select></label>
        <template v-if="draft.transport === 'websocket' && websocketAllowed"><label>WebSocket 路径<input v-model="draft.wsPath" required pattern="/.*" data-field="/transport/path" /></label><label>WebSocket Host（可选）<input v-model="draft.wsHost" data-field="/transport/host" /></label></template>
        <template v-if="draft.security !== 'none'"><label>服务器名称（SNI）<input v-model="draft.serverName" required data-field="/security/server_name" /></label><label>ALPN（可选）<input v-model="draft.alpn" placeholder="h2, http/1.1" data-field="/security/alpn" /></label><label>客户端指纹{{ draft.security === 'tls' ? '（可选）' : '' }}<input v-model="draft.fingerprint" :required="draft.security === 'reality'" placeholder="chrome" data-field="/security/client_fingerprint" /></label><label v-if="draft.security === 'tls'" class="checkbox-label"><input v-model="draft.verifyCertificate" type="checkbox" data-field="/security/verify_certificate" />验证服务器证书</label></template>
        <template v-if="draft.security === 'reality'"><SecretField v-model="draft.publicKey" label="REALITY 公钥" path="/security/public_key" :editing="editing" /><SecretField v-model="draft.shortID" label="REALITY Short ID" path="/security/short_id" :editing="editing" allow-empty /></template>
      </div>
      <p v-if="draft.security === 'tls' && !draft.verifyCertificate" class="notice warning">已关闭证书验证，请确认这符合节点配置。</p>
      <p v-if="editing" class="hint">编辑时留空可清除已设置的 ALPN 或 TLS 客户端指纹；REALITY 客户端指纹为必填项。</p>
    </fieldset>
    <fieldset :disabled="busy || blocked"><legend>功能选项</legend><div class="form-grid"><label>UDP<select v-model="draft.udp"><option value="">未指定</option><option value="true">启用</option><option value="false">关闭</option></select></label><label>连接复用<select v-model="draft.multiplex"><option value="">未指定</option><option value="true">启用</option><option value="false">关闭</option></select></label></div><label v-if="draft.protocol === 'vless' && draft.security !== 'none' && draft.transport === 'native_tcp'" class="checkbox-label"><input v-model="draft.vision" type="checkbox" />使用 xtls-rprx-vision</label><p class="hint">保存仅进行配置结构验证；功能组合需通过对应真实内核验证后才能确认兼容。</p></fieldset>
    <div class="actions form-footer"><RouterLink class="button secondary" :to="editing ? `/nodes/${$route.params.id}` : '/nodes'">取消</RouterLink><button :disabled="busy || blocked">{{ busy ? '正在保存…' : editing ? '保存新修订' : '创建节点' }}</button></div>
  </form>
</template>
