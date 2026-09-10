import type { Schema } from '../api/client'
import { DraftError, splitList } from './node-form.ts'

export type OrchestrationKind = 'chain' | 'policy_group'
export type OrchestrationResource = Schema<'ChainResource'> | Schema<'PolicyGroupResource'>
export type OrchestrationRead = Schema<'ChainReadResponse'> | Schema<'PolicyGroupResponse'>
export type OrchestrationList = Schema<'ChainListResponse'> | Schema<'PolicyGroupListResponse'>
export const resourcePaths = { chain: '/chains', policy_group: '/policy-groups' } as const
export const resourceLabels = { chain: '链路', policy_group: '策略组' } as const
export const strategyLabels: Record<Schema<'PolicyStrategy'>, string> = {
  fixed: '固定选择', manual_select: '客户端手动选择', latency_best: '最低延迟', round_robin: '轮询',
}
export const strategyDescriptions: Record<Schema<'PolicyStrategy'>, string> = {
  fixed: '发布时选定默认成员。', manual_select: '由客户端在成员中手动选择，默认成员作为初始选择。',
  latency_best: '由客户端根据运行时观测选择成员；可用性取决于目标内核与客户端。',
  round_robin: '按成员顺序轮询；可用性取决于目标内核与客户端。',
}
export type MemberOption = { ref: Schema<'MemberRef'>; name: string }
type MetadataDraft = { name: string; tags: string; enabled: boolean }
export type ChainDraft = MetadataDraft & { hops: [string, string] }
export type PolicyDraft = MetadataDraft & {
  strategy: Schema<'PolicyStrategy'>; members: Schema<'MemberRef'>[]; defaultMember: string
  health: Schema<'PolicyHealthCheck'>
}
export const memberKey = (member: Schema<'MemberRef'>) => `${member.kind}:${member.resource_id}`
export const memberPath = (member: Schema<'MemberRef'>) => `${member.kind === 'node' ? '/nodes' : '/chains'}/${member.resource_id}`
const metadataDraft = (metadata?: Schema<'ResourceMetadata'>): MetadataDraft => ({ name: metadata?.name ?? '', tags: metadata?.tags.join(', ') ?? '', enabled: metadata?.enabled ?? true })
export const newChainDraft = (): ChainDraft => ({ ...metadataDraft(), hops: ['', ''] })
export const newPolicyDraft = (): PolicyDraft => ({ ...metadataDraft(), strategy: 'manual_select', members: [], defaultMember: '', health: { enabled: false } })
export function chainDraftFromResource(resource: Schema<'ChainResource'>): ChainDraft {
  return { ...metadataDraft(resource.metadata), hops: [resource.chain.hops[0].node_id, resource.chain.hops[1].node_id] }
}
export function policyDraftFromResource(resource: Schema<'PolicyGroupResource'>): PolicyDraft {
  return { ...metadataDraft(resource.metadata), strategy: resource.policy_group.strategy,
    members: resource.policy_group.members.map(member => ({ ...member })), defaultMember: memberKey(resource.policy_group.default_member), health: { ...resource.policy_group.health_check } }
}
export function swapHops(draft: ChainDraft) { draft.hops = [draft.hops[1], draft.hops[0]] }
export function removeMember(draft: PolicyDraft, index: number) {
  const [removed] = draft.members.splice(index, 1)
  if (removed && memberKey(removed) === draft.defaultMember) draft.defaultMember = ''
}
export function moveMember(draft: PolicyDraft, index: number, delta: number) {
  const next = index + delta
  if (next < 0 || next >= draft.members.length) return
  const [member] = draft.members.splice(index, 1); draft.members.splice(next, 0, member)
}
export function enableHealthCheck(draft: PolicyDraft) {
  if (!draft.health.enabled) return
  draft.health.url ??= ''; draft.health.interval_ms ??= 300000; draft.health.timeout_ms ??= 5000; draft.health.tolerance_ms ??= 50
}
function healthRequest(health: Schema<'PolicyHealthCheck'>): Schema<'PolicyHealthCheck'> {
  if (!health.enabled) return { enabled: false }
  let url: URL
  try { url = new URL(health.url ?? '') } catch { throw new DraftError('健康检查地址必须是完整的 HTTPS 地址。') }
  if (!/^https:\/\/[^\s@#]+$/.test(health.url ?? '') || (health.url?.length ?? 0) > 2048 || !url.hostname || url.username || url.password || url.hash || url.port === '0' || /:\//.test(url.host)) throw new DraftError('健康检查地址必须使用 HTTPS，且不能含认证信息、片段或无效端口。')
  for (const [key, label, min, max] of [['interval_ms', '检查间隔', 1000, 86400000], ['timeout_ms', '检查超时', 100, 60000], ['tolerance_ms', '延迟容差', 0, 60000]] as const) {
    const value = health[key]
    if (value === undefined || !Number.isInteger(value) || value < min || value > max) throw new DraftError(`${label}必须是 ${min}–${max} 毫秒的整数。`)
  }
  return { enabled: true, url: health.url, interval_ms: health.interval_ms, timeout_ms: health.timeout_ms, tolerance_ms: health.tolerance_ms }
}
function metadataRequest(draft: MetadataDraft) {
  if (!draft.name.trim()) throw new DraftError('请填写名称。')
  return { name: draft.name, tags: splitList(draft.tags), enabled: draft.enabled }
}
function metadataPatch(draft: MetadataDraft, baseline: Schema<'ResourceMetadata'>) {
  const current = metadataRequest(draft)
  const result: Partial<typeof current> = {}
  if (current.name !== baseline.name) result.name = current.name
  if (JSON.stringify(current.tags) !== JSON.stringify(baseline.tags)) result.tags = current.tags
  if (current.enabled !== baseline.enabled) result.enabled = current.enabled
  return result
}
function requireChange<T extends object>(request: T): T {
  if (!Object.keys(request).length) throw new DraftError('没有需要保存的更改。')
  return request
}
export function chainCreateRequest(draft: ChainDraft, options: MemberOption[]): Schema<'ChainCreateRequest'> {
  if (!draft.hops[0] || !draft.hops[1] || draft.hops[0] === draft.hops[1]) throw new DraftError('请选择两个不同的节点，依次作为第一跳和最终出口。')
  if (draft.hops.some(id => !options.some(option => option.ref.kind === 'node' && option.ref.resource_id === id))) throw new DraftError('链路只允许引用已启用节点，请刷新候选资源并更换不可用节点。')
  return { ...metadataRequest(draft), hops: draft.hops.map(node_id => ({ node_id })), failure_policy: 'fail_closed' }
}
export function chainPatchRequest(draft: ChainDraft, baseline: Schema<'ChainResource'>, options: MemberOption[]): Schema<'ChainPatchRequest'> {
  const current = chainCreateRequest(draft, options)
  const result: Schema<'ChainPatchRequest'> = metadataPatch(draft, baseline.metadata)
  if (JSON.stringify(current.hops) !== JSON.stringify(baseline.chain.hops)) result.hops = current.hops
  return requireChange(result)
}
export function policyCreateRequest(draft: PolicyDraft, options: MemberOption[]): Schema<'PolicyGroupCreateRequest'> {
  if (!draft.members.length || draft.members.length > 200) throw new DraftError('策略组需要 1–200 个节点或链路成员。')
  if (new Set(draft.members.map(member => member.resource_id)).size !== draft.members.length) throw new DraftError('策略组不能包含重复成员。')
  if (draft.members.some(member => !options.some(option => memberKey(option.ref) === memberKey(member)))) throw new DraftError('成员必须为已启用节点或链路，请刷新候选资源并移除不可用成员。')
  const defaultMember = draft.members.find(member => memberKey(member) === draft.defaultMember)
  if (!defaultMember) throw new DraftError('默认成员必须从当前已选成员中选择。')
  return { ...metadataRequest(draft), policy_group: { schema_version: 1, strategy: draft.strategy, members: draft.members.map(member => ({ ...member })), default_member: { ...defaultMember }, health_check: healthRequest(draft.health), on_unavailable: 'fail_closed' } }
}
export function policyPatchRequest(draft: PolicyDraft, baseline: Schema<'PolicyGroupResource'>, options: MemberOption[]): Schema<'PolicyGroupPatchRequest'> {
  const current = policyCreateRequest(draft, options).policy_group
  const before = baseline.policy_group
  const group: Schema<'PolicyGroupPatch'> = {}
  if (current.strategy !== before.strategy) group.strategy = current.strategy
  if (JSON.stringify(current.members) !== JSON.stringify(before.members)) group.members = current.members
  if (memberKey(current.default_member) !== memberKey(before.default_member)) group.default_member = current.default_member
  if (JSON.stringify(current.health_check) !== JSON.stringify(healthRequest(before.health_check))) group.health_check = current.health_check
  return requireChange({ ...metadataPatch(draft, baseline.metadata), ...(Object.keys(group).length ? { policy_group: group } : {}) })
}
