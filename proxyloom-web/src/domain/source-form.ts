import type { Schema } from '../api/client'
import { DraftError, splitList } from './node-form'
import type { SecretDraft } from './node-form'

const secret = (keep = false): SecretDraft => ({ mode: keep ? 'keep' : 'replace', value: '', canKeep: keep })
export type SourceDraft = {
  name: string; tags: string; enabled: boolean; url: SecretDraft
  format: Schema<'ImportFormat'>; authKind: Schema<'SourceAuth'>['kind']
  token: SecretDraft; username: SecretDraft; password: SecretDraft
  refresh: Schema<'SourceRefreshPolicy'>; limits: Schema<'SourceFetchLimits'>
}

export function newSourceDraft(): SourceDraft {
  return {
    name: '', tags: '', enabled: true, url: secret(), format: 'auto', authKind: 'none',
    token: secret(), username: secret(), password: secret(),
    refresh: { enabled: false, interval_seconds: 3600, commit_mode: 'manual', missing_policy: 'retain' },
    limits: { timeout_ms: 15000, max_compressed_bytes: 10485760, max_decoded_bytes: 10485760, max_redirects: 3 },
  }
}

export function sourceDraftFromResource(resource: Schema<'SourceResource'>): SourceDraft {
  const { metadata, source } = resource
  const draft = newSourceDraft()
  Object.assign(draft, { name: metadata.name, tags: metadata.tags.join(', '), enabled: metadata.enabled,
    url: secret(source.has_url), format: source.format, authKind: source.auth.kind,
    refresh: { ...source.refresh_policy }, limits: { ...source.fetch_limits } })
  if (source.auth.kind === 'bearer') draft.token = secret(source.auth.has_token)
  if (source.auth.kind === 'basic') { draft.username = secret(source.auth.has_username); draft.password = secret(source.auth.has_password) }
  return draft
}

export function clearSourceSecrets(draft: SourceDraft) {
  for (const item of [draft.url, draft.token, draft.username, draft.password]) item.value = ''
}
export function resetSourceAuth(draft: SourceDraft) { draft.token = secret(); draft.username = secret(); draft.password = secret() }

function valueOf(draft: SecretDraft, label: string, editing: boolean): string | null | undefined {
  if (editing && draft.mode === 'keep' && draft.canKeep) return undefined
  if (editing && draft.mode === 'clear') return null
  if (draft.mode !== 'replace' || !draft.value || /^(?:\*+|REDACTED|<redacted>|\[REDACTED\]|•+)$/i.test(draft.value)) throw new DraftError(`${label}需要填写新值，不能提交显示遮罩。`)
  return draft.value
}

function sourceWrite(draft: SourceDraft, editing: boolean): Schema<'SourcePatch'> {
  const url = valueOf(draft.url, '来源地址', editing)
  if (url) {
    let parsed: URL
    try { parsed = new URL(url) } catch { throw new DraftError('来源地址必须是完整的 HTTP 或 HTTPS 地址。') }
    if (!['http:', 'https:'].includes(parsed.protocol) || parsed.hash || /\s/.test(url)) throw new DraftError('来源地址必须使用 HTTP 或 HTTPS，且不能包含空白或片段。')
  }
  const auth: Schema<'SourceAuthPatch'> = draft.authKind === 'none' ? { kind: 'none' }
    : draft.authKind === 'bearer' ? { kind: 'bearer', token: valueOf(draft.token, 'Bearer 令牌', editing) }
      : { kind: 'basic', username: valueOf(draft.username, '认证用户名', editing), password: valueOf(draft.password, '认证密码', editing) }
  if (!Number.isInteger(draft.refresh.interval_seconds) || draft.refresh.interval_seconds < 60 || draft.refresh.interval_seconds > 2592000) throw new DraftError('刷新间隔必须是 60–2592000 秒的整数。')
  return { ...(url !== undefined ? { url } : {}), format: draft.format, auth, refresh_policy: { ...draft.refresh }, fetch_limits: { ...draft.limits } }
}

export function sourceCreateRequest(draft: SourceDraft): Schema<'SourceCreateRequest'> {
  const source = sourceWrite(draft, false) as Schema<'SourceWrite'>
  return { name: draft.name, tags: splitList(draft.tags), enabled: draft.enabled, source: { ...source, schema_version: 1 } }
}

export function sourcePatchRequest(draft: SourceDraft, baseline: Schema<'SourceResource'>): Schema<'SourcePatchRequest'> {
  const source = sourceWrite(draft, true)
  const before = baseline.source
  if (source.format === before.format) delete source.format
  if (JSON.stringify(source.refresh_policy) === JSON.stringify(before.refresh_policy)) delete source.refresh_policy
  if (JSON.stringify(source.fetch_limits) === JSON.stringify(before.fetch_limits)) delete source.fetch_limits
  if (source.auth?.kind === before.auth.kind && Object.values(source.auth).filter(value => value !== undefined).length === 1) delete source.auth
  const result: Schema<'SourcePatchRequest'> = {}
  if (draft.name !== baseline.metadata.name) result.name = draft.name
  const tags = draft.tags === baseline.metadata.tags.join(', ') ? baseline.metadata.tags : splitList(draft.tags)
  if (JSON.stringify(tags) !== JSON.stringify(baseline.metadata.tags)) result.tags = tags
  if (draft.enabled !== baseline.metadata.enabled) result.enabled = draft.enabled
  if (Object.keys(source).length) result.source = source
  if (!Object.keys(result).length) throw new DraftError('没有需要保存的更改。')
  return result
}

export const sourceFormatLabels: Record<Schema<'ImportFormat'>, string> = {
  auto: '自动识别', uri_list: 'URI 分享链接列表', base64_uri_list: 'Base64 URI 列表',
  xray_json: 'Xray JSON', singbox_json: 'sing-box JSON', mihomo_yaml: 'Mihomo YAML',
}
export const jobStateLabels: Record<Schema<'JobState'>, string> = {
  queued: '等待执行', leased: '已分配', running: '正在刷新', succeeded: '刷新完成', failed: '刷新失败', canceled: '已取消', timed_out: '已超时',
}
export function runningJob(job: Schema<'Job'> | null) { return !!job && ['queued', 'leased', 'running'].includes(job.state) }
