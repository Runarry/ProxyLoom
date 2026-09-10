import type { OrchestrationKind, OrchestrationResource, OrchestrationRead, OrchestrationList } from './orchestration-form'
import { resourcePaths, resourceLabels, strategyLabels } from './orchestration-form'
import type { NetworkKind, NetworkResource, NetworkRead, NetworkList } from './network-draft'
import { networkPaths, networkAPIPaths, networkLabels } from './network-draft'
export type CatalogKind = OrchestrationKind | NetworkKind
export type CatalogResource = OrchestrationResource | NetworkResource
export type CatalogRead = OrchestrationRead | NetworkRead
export type CatalogList = OrchestrationList | NetworkList
export const catalogPaths = { ...resourcePaths, ...networkPaths }
export const catalogAPIPaths = { ...resourcePaths, ...networkAPIPaths }
export const catalogLabels = { ...resourceLabels, ...networkLabels }
export function resourceSummary(resource: CatalogResource) {
  if ('chain' in resource) return `${resource.chain.hops.length} 跳 · 第一跳 → 最终出口`
  if ('policy_group' in resource) return `${strategyLabels[resource.policy_group.strategy]} · ${resource.policy_group.members.length} 个成员`
  if ('routing_profile' in resource) return `${resource.routing_profile.rules.length} 条有序规则`
  if ('dns_profile' in resource) return `${resource.dns_profile.resolvers.length} 个主解析器 · ${resource.dns_profile.rules.length} 条规则`
  return `${resource.rule_set.entries.length} 条域名 / CIDR 规则`
}
