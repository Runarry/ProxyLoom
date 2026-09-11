import { api, queryString } from './client'
import type { Schema } from './client'
import type { MemberOption } from '../domain/orchestration-form'

export async function loadMemberOptions(includeChains: boolean, signal: AbortSignal): Promise<MemberOption[]> {
  async function collection(path: '/nodes' | '/chains'): Promise<MemberOption[]> {
    const options: MemberOption[] = []
    let cursor = ''
    do {
      const response = await api<Schema<'NodeListResponse'> | Schema<'ChainListResponse'>>(`${path}${queryString({ enabled: true, limit: 200, cursor })}`, { signal })
      for (const resource of response.body.data) if (resource.metadata.enabled) options.push({ ref: { type: 'resource_ref', kind: path === '/nodes' ? 'node' : 'chain', resource_id: resource.metadata.resource_id }, name: resource.metadata.name })
      cursor = response.body.page.next_cursor ?? ''
    } while (cursor && !signal.aborted)
    return options
  }
  const results = await Promise.all(includeChains ? [collection('/nodes'), collection('/chains')] : [collection('/nodes')])
  return results.flat()
}
