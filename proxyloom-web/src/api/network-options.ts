import { api, queryString } from './client'
import type { Schema } from './client'
import type { NetworkOptions, TargetOption, RuleSetOption } from '../domain/network-draft'

export async function loadNetworkOptions(signal: AbortSignal): Promise<NetworkOptions> {
  async function collection(path: string) {
    const resources: Schema<'ResourceMetadata'>[] = []
    let cursor = ''
    do {
      const response = await api<Schema<'NodeListResponse'> | Schema<'ChainListResponse'> | Schema<'PolicyGroupListResponse'> | Schema<'RuleSetListResponse'>>(`${path}${queryString({ enabled: true, limit: 200, cursor })}`, { signal })
      resources.push(...response.body.data.filter(resource => resource.metadata.enabled).map(resource => resource.metadata))
      cursor = response.body.page.next_cursor ?? ''
    } while (cursor && !signal.aborted)
    return resources
  }
  const [nodes, chains, groups, rules] = await Promise.all([collection('/nodes'), collection('/chains'), collection('/policy-groups'), collection('/rule-sets')])
  const targets: TargetOption[] = [...nodes, ...chains, ...groups].map(metadata => ({ name: metadata.name, ref: { type: 'resource_ref', kind: metadata.kind as Schema<'RoutingResourceRef'>['kind'], resource_id: metadata.resource_id } }))
  const ruleSets: RuleSetOption[] = rules.map(metadata => ({ id: metadata.resource_id, name: metadata.name }))
  return { targets, ruleSets }
}
