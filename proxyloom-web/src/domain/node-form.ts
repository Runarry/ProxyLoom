import type { Schema } from '../api/client'

export type Protocol = Schema<'Protocol'>
export const protocols: { value: Protocol; label: string }[] = [
  { value: 'shadowsocks', label: 'Shadowsocks' }, { value: 'vmess', label: 'VMess' },
  { value: 'vless', label: 'VLESS' }, { value: 'trojan', label: 'Trojan' },
  { value: 'socks5', label: 'SOCKS5' }, { value: 'http', label: 'HTTP' },
]
export type SecretDraft = { mode: 'keep' | 'replace' | 'clear'; value: string; canKeep: boolean }
const secret = (keep = false): SecretDraft => ({ mode: keep ? 'keep' : 'replace', value: '', canKeep: keep })
export type NodeDraft = {
  name: string; tags: string; enabled: boolean; protocol: Protocol; host: string; port: number
  method: Schema<'ShadowsocksMethod'>; cipher: Schema<'VMessCipher'>; authenticated: boolean
  password: SecretDraft; uuid: SecretDraft; username: SecretDraft
  transport: 'native_tcp' | 'websocket'; wsPath: string; wsHost: string
  security: 'none' | 'tls' | 'reality'; serverName: string; verifyCertificate: boolean
  alpn: string; fingerprint: string; publicKey: SecretDraft; shortID: SecretDraft
  udp: '' | 'true' | 'false'; multiplex: '' | 'true' | 'false'; vision: boolean
}

export class DraftError extends Error { constructor(message: string) { super(message); this.name = 'DraftError' } }

export function newDraft(protocol: Protocol = 'shadowsocks'): NodeDraft {
  return {
    name: '', tags: '', enabled: true, protocol, host: '', port: 443,
    method: 'aes-128-gcm', cipher: 'auto', authenticated: false,
    password: secret(), uuid: secret(), username: secret(),
    transport: 'native_tcp', wsPath: '/', wsHost: '', security: protocol === 'trojan' ? 'tls' : 'none',
    serverName: '', verifyCertificate: true, alpn: '', fingerprint: '', publicKey: secret(), shortID: secret(),
    udp: '', multiplex: '', vision: false,
  }
}

export function draftFromResource(resource: Schema<'NodeResource'>): NodeDraft {
  const { node, metadata } = resource
  const draft = newDraft(node.protocol)
  Object.assign(draft, { name: metadata.name, tags: metadata.tags.join(', '), enabled: metadata.enabled, host: node.endpoint.host, port: node.endpoint.port })
  const auth = node.auth
  if (auth.kind === 'method_password') { draft.method = auth.method; draft.password = secret(auth.has_password) }
  if (auth.kind === 'vmess_aead') { draft.cipher = auth.cipher; draft.uuid = secret(auth.has_uuid) }
  if (auth.kind === 'uuid') draft.uuid = secret(auth.has_uuid)
  if (auth.kind === 'password') draft.password = secret(auth.has_password)
  if (auth.kind === 'username_password') { draft.authenticated = true; draft.username = secret(auth.has_username); draft.password = secret(auth.has_password) }
  draft.transport = node.transport.kind
  if (node.transport.kind === 'websocket') { draft.wsPath = node.transport.path; draft.wsHost = node.transport.host ?? '' }
  draft.security = node.security.mode
  if (node.security.mode !== 'none') {
    draft.serverName = node.security.server_name
    draft.alpn = node.security.alpn?.join(', ') ?? ''
    draft.fingerprint = node.security.client_fingerprint ?? ''
    if (node.security.mode === 'tls') draft.verifyCertificate = node.security.verify_certificate
    else { draft.publicKey = secret(node.security.has_public_key); draft.shortID = secret(node.security.has_short_id) }
  }
  draft.udp = node.features.udp === undefined ? '' : node.features.udp ? 'true' : 'false'
  draft.multiplex = node.features.multiplex === undefined ? '' : node.features.multiplex ? 'true' : 'false'
  draft.vision = node.features.protocol_variant === 'xtls-rprx-vision'
  return draft
}

// Rebuilding instead of hiding inputs prevents any previous protocol's fields or secrets from leaking into the request.
export function changeProtocol(draft: NodeDraft, protocol: Protocol): NodeDraft {
  return { ...newDraft(protocol), name: draft.name, tags: draft.tags, enabled: draft.enabled, host: draft.host, port: draft.port }
}
export function resetAuthentication(draft: NodeDraft) {
  draft.username = secret(); draft.password = secret(); draft.uuid = secret()
}
export function resetSecurity(draft: NodeDraft) {
  draft.serverName = ''; draft.verifyCertificate = true; draft.alpn = ''; draft.fingerprint = ''
  draft.publicKey = secret(); draft.shortID = secret(); draft.vision = false
  if (draft.security === 'reality') { draft.transport = 'native_tcp'; draft.wsPath = '/'; draft.wsHost = ''; draft.fingerprint = 'chrome' }
}
export function clearDraftSecrets(draft: NodeDraft) {
  for (const value of [draft.password, draft.uuid, draft.username, draft.publicKey, draft.shortID]) value.value = ''
}
export function splitList(value: string) { return [...new Set(value.split(/[,，\n]/).map(item => item.trim()).filter(Boolean))] }

function secretValue(value: SecretDraft, editing: boolean, label: string, allowEmpty = false): string | null | undefined {
  if (value.mode === 'keep') {
    if (!editing || !value.canKeep) throw new DraftError(`${label}需要输入新值。`)
    return undefined
  }
  if (value.mode === 'clear') {
    if (!editing) throw new DraftError(`${label}不能在创建时清除。`)
    return null
  }
  if ((!allowEmpty && !value.value) || ['********', 'REDACTED', '<redacted>', '[REDACTED]'].includes(value.value)) throw new DraftError(`${label}需要填写真实值，不能提交显示遮罩。`)
  return value.value
}

function draftNode(draft: NodeDraft, editing: boolean): Schema<'NodePatch'> {
  const credential = (value: SecretDraft, label: string, allowEmpty = false) => secretValue(value, editing, label, allowEmpty)
  let auth: Schema<'NodeAuthPatch'>
  switch (draft.protocol) {
    case 'shadowsocks': auth = { kind: 'method_password', method: draft.method, password: credential(draft.password, '密码') }; break
    case 'vmess': auth = { kind: 'vmess_aead', cipher: draft.cipher, uuid: credential(draft.uuid, 'UUID') }; break
    case 'vless': auth = { kind: 'uuid', uuid: credential(draft.uuid, 'UUID') }; break
    case 'trojan': auth = { kind: 'password', password: credential(draft.password, '密码') }; break
    default: auth = draft.authenticated ? { kind: 'username_password', username: credential(draft.username, '认证用户名'), password: credential(draft.password, '密码') } : { kind: 'none' }
  }
  const canWebsocket = ['vmess', 'vless', 'trojan'].includes(draft.protocol) && draft.security !== 'reality' && !draft.vision
  const transport: Schema<'NodeTransport'> = canWebsocket && draft.transport === 'websocket'
    ? { kind: 'websocket', path: draft.wsPath, ...(draft.wsHost ? { host: draft.wsHost } : {}) } : { kind: 'native_tcp' }
  const alpn = splitList(draft.alpn)
  let security: Schema<'NodeSecurityPatch'> = { mode: 'none' }
  if (draft.protocol !== 'shadowsocks' && draft.security === 'tls') security = {
    mode: 'tls', server_name: draft.serverName, verify_certificate: draft.verifyCertificate,
    ...(alpn.length ? { alpn } : {}), ...(draft.fingerprint ? { client_fingerprint: draft.fingerprint } : {}),
  }
  if (draft.protocol === 'vless' && draft.security === 'reality') security = {
    mode: 'reality', server_name: draft.serverName, client_fingerprint: draft.fingerprint,
    public_key: credential(draft.publicKey, 'REALITY 公钥'), short_id: credential(draft.shortID, 'REALITY Short ID', true),
    ...(alpn.length ? { alpn } : {}),
  }
  if (security.mode === 'reality' && !draft.fingerprint) throw new DraftError('REALITY 客户端指纹为必填项，不能清除。')
  if (draft.protocol === 'trojan' && security.mode !== 'tls') throw new DraftError('Trojan 必须使用 TLS。')
  const features: Schema<'NodeFeatures'> = {}
  if (draft.udp !== '') features.udp = draft.udp === 'true'
  if (draft.multiplex !== '') features.multiplex = draft.multiplex === 'true'
  if (draft.protocol === 'vless' && draft.vision) {
    if (security.mode === 'none') throw new DraftError('Vision 需要 TLS 或 REALITY。')
    features.protocol_variant = 'xtls-rprx-vision'
  }
  if (!Number.isInteger(draft.port) || draft.port < 1 || draft.port > 65535) throw new DraftError('端口必须是 1–65535 的整数。')
  return { protocol: draft.protocol, endpoint: { host: draft.host, port: draft.port }, auth, transport, security, features }
}

export function createRequest(draft: NodeDraft): Schema<'NodeCreateRequest'> {
  // All required secrets are materialized by draftNode in create mode; null/keep are rejected there.
  const node = draftNode(draft, false) as Schema<'NodeCreate'>
  return { name: draft.name, tags: splitList(draft.tags), enabled: draft.enabled, node: { ...node, schema_version: 1, extensions: {} } }
}
export function patchRequest(draft: NodeDraft, baseline: Schema<'NodeResource'>): Schema<'NodePatchRequest'> {
  const node = draftNode(draft, true)
  const security = node.security
  const oldSecurity = baseline.node.security
  if (security && security.mode === oldSecurity.mode && security.mode !== 'none' && oldSecurity.mode !== 'none') {
    const alpn = splitList(draft.alpn)
    if (draft.alpn === (oldSecurity.alpn?.join(', ') ?? '') || JSON.stringify(alpn) === JSON.stringify(oldSecurity.alpn ?? [])) delete security.alpn
    else security.alpn = alpn
    if (draft.fingerprint === (oldSecurity.client_fingerprint ?? '')) delete security.client_fingerprint
    else if (security.mode === 'tls') security.client_fingerprint = draft.fingerprint || null
  }
  const tags = draft.tags === baseline.metadata.tags.join(', ') ? [...baseline.metadata.tags] : splitList(draft.tags)
  return { name: draft.name, tags, enabled: draft.enabled, node }
}
