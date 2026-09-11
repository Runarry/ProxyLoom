import type { Schema } from '../api/client'
import { DraftError, splitList } from './node-form.ts'
import { normalizeDomain, normalizeCIDR } from './network-form.ts'

export type NetworkKind = 'routing_profile' | 'rule_set' | 'dns_profile'
export type NetworkResource = Schema<'RoutingProfileResource'> | Schema<'RuleSetResource'> | Schema<'DNSProfileResource'>
export type NetworkRead = Schema<'RoutingProfileResponse'> | Schema<'RuleSetResponse'> | Schema<'DNSProfileResponse'>
export type NetworkList = Schema<'RoutingProfileListResponse'> | Schema<'RuleSetListResponse'> | Schema<'DNSProfileListResponse'>
export type TargetOption = { ref: Schema<'RoutingResourceRef'>; name: string }
export type RuleSetOption = { id: string; name: string }
export type NetworkOptions = { targets: TargetOption[]; ruleSets: RuleSetOption[] }
export type NetworkMetadata = { name: string; tags: string; enabled: boolean }
export const networkPaths = { routing_profile: '/routing', rule_set: '/rule-sets', dns_profile: '/dns' } as const
export const networkAPIPaths = { routing_profile: '/routing-profiles', rule_set: '/rule-sets', dns_profile: '/dns-profiles' } as const
export const networkLabels = { routing_profile: '路由配置', rule_set: '规则集', dns_profile: 'DNS 配置' } as const
export const targetKey = (target: Schema<'TargetRef'>) => target.type === 'builtin' ? `builtin:${target.builtin}` : `${target.kind}:${target.resource_id}`
export function sameValue(left: unknown, right: unknown): boolean {
  if (left === right) return true
  if (Array.isArray(left) && Array.isArray(right)) return left.length === right.length && left.every((item, index) => sameValue(item, right[index]))
  if (!left || !right || typeof left !== 'object' || typeof right !== 'object' || Array.isArray(left) || Array.isArray(right)) return false
  const a = left as Record<string, unknown>, b = right as Record<string, unknown>
  return Object.keys(a).length === Object.keys(b).length && Object.keys(a).every(key => Object.hasOwn(b, key) && sameValue(a[key], b[key]))
}
export function targetLabel(target: Schema<'TargetRef'>, options: TargetOption[] = []) {
  return target.type === 'builtin' ? target.builtin === 'direct' ? '直连' : '拒绝连接' : options.find(option => targetKey(option.ref) === targetKey(target))?.name ?? `${target.kind} · ${target.resource_id}`
}
export const newRouting = (): Schema<'RoutingProfile'> => ({ schema_version: 1, rules: [], final: { type: 'builtin', builtin: 'reject' }, domain_resolution_mode: 'preserve_domain' })
export const newDNS = (): Schema<'DNSProfile'> => ({ schema_version: 1, bootstrap: [{ resolver_id: 'bootstrap', kind: 'local' }], resolvers: [{ resolver_id: 'local', kind: 'local' }], rules: [], final_resolver: 'local' })
export const newRoutingRule = (): Schema<'RoutingRule'> => ({ enabled: true, comment: '', match: {}, action: { type: 'builtin', builtin: 'reject' } })
export const newDNSRule = (resolver_id: string): Schema<'DNSRule'> => ({ enabled: true, comment: '', match: {}, resolver_id })
export function moveRule<T>(rules: T[], index: number, delta: number) {
  const next = index + delta
  if (next < 0 || next >= rules.length) return
  const [item] = rules.splice(index, 1); rules.splice(next, 0, item)
}
export function metadataFromResource(resource?: NetworkResource): NetworkMetadata { return { name: resource?.metadata.name ?? '', tags: resource?.metadata.tags.join(', ') ?? '', enabled: resource?.metadata.enabled ?? true } }
export function networkMetadata(draft: NetworkMetadata, baseline?: NetworkResource | null): { name?: string; tags?: string[]; enabled?: boolean } {
  if (!draft.name.trim()) throw new DraftError('请填写名称。')
  const current = { name: draft.name, tags: splitList(draft.tags), enabled: draft.enabled }
  if (!baseline) return current
  return { ...(current.name !== baseline.metadata.name ? { name: current.name } : {}), ...(JSON.stringify(current.tags) !== JSON.stringify(baseline.metadata.tags) ? { tags: current.tags } : {}), ...(current.enabled !== baseline.metadata.enabled ? { enabled: current.enabled } : {}) }
}
function validateTarget(target: Schema<'TargetRef'>, options: NetworkOptions) {
  if (target.type === 'resource_ref' && !options.targets.some(option => targetKey(option.ref) === targetKey(target))) throw new DraftError('动作引用的资源未启用或不可用，请刷新候选资源并重新选择。')
  return { ...target }
}
export function normalizedMatch(match: Schema<'RouteMatch'>, options: NetworkOptions): Schema<'RouteMatch'> {
  const result: Schema<'RouteMatch'> = {}
  for (const key of ['domain_exact', 'domain_suffix'] as const) if (match[key]?.length) result[key] = [...new Set(match[key].map(normalizeDomain))]
  if (match.ip_cidrs?.length) result.ip_cidrs = [...new Set(match.ip_cidrs.map(normalizeCIDR))]
  if (match.rule_set_ids?.length) {
    if (match.rule_set_ids.some(id => !options.ruleSets.some(option => option.id === id))) throw new DraftError('匹配条件引用的规则集未启用或不可用。')
    result.rule_set_ids = [...new Set(match.rule_set_ids)]
  }
  if (match.network?.length) result.network = [...new Set(match.network)]
  if (match.destination_ports?.length) {
    if (match.destination_ports.some(range => !Number.isInteger(range.from) || !Number.isInteger(range.to) || range.from < 1 || range.to > 65535 || range.from > range.to)) throw new DraftError('目标端口必须是 1–65535 的整数，起始端口不能大于结束端口。')
    result.destination_ports = match.destination_ports.map(range => ({ ...range }))
  }
  if (!Object.keys(result).length) throw new DraftError('每条规则至少需要一个匹配条件。')
  return result
}
export function routingRequest(draft: Schema<'RoutingProfile'>, options: NetworkOptions): Schema<'RoutingProfile'> {
  return { schema_version: 1, domain_resolution_mode: draft.domain_resolution_mode, final: validateTarget(draft.final, options), rules: draft.rules.map((rule, index) => {
    try { return { enabled: rule.enabled, comment: rule.comment, action: validateTarget(rule.action, options), match: normalizedMatch(rule.match, options) } }
    catch (error) { throw new DraftError(`路由规则 ${index + 1}：${error instanceof Error ? error.message : '配置无效。'}`) }
  }) }
}
function literalAddress(address: string) {
  return normalizeCIDR(`${address}/${address.includes(':') ? 128 : 32}`).split('/')[0]
}
export function dnsRequest(draft: Schema<'DNSProfile'>, options: NetworkOptions): Schema<'DNSProfile'> {
  const all = [...draft.bootstrap, ...draft.resolvers]
  if (!draft.bootstrap.length || !draft.resolvers.length) throw new DraftError('DNS 至少需要一个引导解析器和一个主解析器。')
  if (all.some(resolver => !/^[a-z][a-z0-9_-]{0,63}$/.test(resolver.resolver_id)) || new Set(all.map(resolver => resolver.resolver_id)).size !== all.length) throw new DraftError('解析器 ID 必须唯一，以小写字母开头，仅含小写字母、数字、下划线和连字符，最多 64 字符。')
  const hasResolver = (id: string) => draft.resolvers.some(resolver => resolver.resolver_id === id)
  if (!hasResolver(draft.final_resolver) || draft.rules.some(rule => !hasResolver(rule.resolver_id))) throw new DraftError('默认解析器和规则解析器必须引用当前已声明的主解析器。')
  const bootstrap: Schema<'BootstrapResolver'>[] = draft.bootstrap.map(resolver => resolver.kind === 'local' ? { ...resolver } : { ...resolver, address: literalAddress(resolver.address) })
  const resolvers: Schema<'DNSResolver'>[] = draft.resolvers.map(resolver => {
    if (resolver.kind === 'local') return { ...resolver }
    if (resolver.kind === 'udp') return { ...resolver, address: literalAddress(resolver.address), outbound: validateTarget(resolver.outbound, options) }
    if (!draft.bootstrap.some(item => item.resolver_id === resolver.bootstrap_resolver_id)) throw new DraftError('HTTPS 解析器必须引用当前已声明的引导解析器。')
    let url: URL
    try { url = new URL(resolver.url) } catch { throw new DraftError('HTTPS 解析地址格式无效。') }
    if (!/^https:\/\/[^\s@#]+$/.test(resolver.url) || !url.hostname || url.port === '0' || resolver.url.length > 2048) throw new DraftError('HTTPS 解析地址不能包含认证信息、片段或无效端口。')
    return { ...resolver, outbound: validateTarget(resolver.outbound, options) }
  })
  return { schema_version: 1, bootstrap, resolvers, final_resolver: draft.final_resolver, rules: draft.rules.map((rule, index) => {
    try { return { ...rule, match: normalizedMatch(rule.match, options) } }
    catch (error) { throw new DraftError(`DNS 规则 ${index + 1}：${error instanceof Error ? error.message : '配置无效。'}`) }
  }) }
}
