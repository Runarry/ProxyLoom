import { api, queryString } from '../api/client'
import type { Schema } from '../api/client'

export type Subscription = Schema<'SubscriptionResource'>
export type Profile = Schema<'SubscriptionProfile'>
export type Target = Schema<'SubscriptionTarget'>
export type Member = { metadata: Schema<'ResourceMetadata'> }
export const publicationState: Record<string, string> = { not_ready: '尚未发布', active: '可下载', blocked: '已阻断', historical: '历史版本' }
export const compileState: Record<string, string> = { queued: '等待编译', compiling: '正在编译', validating: '等待内核校验', ready: '校验通过，等待确认发布', failed: '编译或校验失败', obsolete: '输入已变化，需要重新编译' }
export const emptyProfile = (): Profile => ({ schema_version: 1, members: { include_ids: [], exclude_ids: [], selector: { all_tags: [], any_tags: [], none_tags: [] } }, routing_profile_id: '', dns_profile_id: '', targets: [], publish_policy: 'strict_all_targets' })
export async function collection<T>(path: string, signal: AbortSignal): Promise<T[]> {
  const items: T[] = []; let cursor = ''
  do {
    const response = await api<{ data: T[]; page: Schema<'PageInfo'> }>(`${path}${queryString({ limit: 200, cursor })}`, { signal })
    items.push(...response.body.data); cursor = response.body.page.next_cursor ?? ''
  } while (cursor && !signal.aborted)
  return items
}
export function targetKeys(profile: Profile) { return profile.targets.filter(target => target.enabled !== false).map(target => target.key) }
