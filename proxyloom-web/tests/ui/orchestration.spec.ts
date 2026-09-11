import { test, expect, type Page, type Route } from '@playwright/test'
import { randomUUID } from 'node:crypto'

const identity = { user_id: randomUUID(), username: 'fixture', role: 'administrator', session_expires_at: '2099-01-01T00:00:00Z', csrf_token: 'x'.repeat(43) }
const metadata = (name: string, kind: string, enabled = true) => ({ resource_id: randomUUID(), scope_id: randomUUID(), kind, schema_version: 1, security_epoch: '1', name, tags: [], enabled, revision: '1' })
const first = { metadata: metadata('First fixture', 'node') }
const exit = { metadata: metadata('Exit fixture', 'node') }
const disabled = { metadata: metadata('Disabled fixture', 'node', false) }
const chainFixture = () => ({ metadata: metadata('Chain fixture', 'chain'), chain: { schema_version: 1, hops: [{ node_id: first.metadata.resource_id }, { node_id: exit.metadata.resource_id }], failure_policy: 'fail_closed' } })
const member = (kind: 'node' | 'chain', resource_id: string) => ({ type: 'resource_ref' as const, kind, resource_id })
const groupFixture = () => ({ metadata: metadata('Group fixture', 'policy_group'), policy_group: { schema_version: 1, strategy: 'manual_select', members: [member('node', first.metadata.resource_id)], default_member: member('node', first.metadata.resource_id), health_check: { enabled: false }, on_unavailable: 'fail_closed' }, diagnostics: [] })
const respond = (route: Route, data: unknown, status = 200, extra = {}, etag = '"r1"') => route.fulfill({ status, json: { request_id: 'ui-orchestration', data, ...extra }, headers: { ETag: etag, 'Cache-Control': 'no-store' } })
const fail = (route: Route, status: number, code: string) => route.fulfill({ status, json: { request_id: 'ui-orchestration', error: { code, message: 'Synthetic fixed error', details: [] } } })
async function mock(page: Page, handler: (route: Route, path: string) => Promise<boolean | void>) {
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    if (path === '/auth/me') return respond(route, identity)
    if (await handler(route, path)) return
    if (path === '/nodes') return respond(route, [first, exit, disabled], 200, { page: { limit: 200 } })
    if (path === '/chains' || path === '/policy-groups') return respond(route, [], 200, { page: { limit: 50 } })
    for (const node of [first, exit, disabled]) if (path === `/nodes/${node.metadata.resource_id}`) return respond(route, node)
    return fail(route, 404, 'RESOURCE_NOT_FOUND')
  })
}

test('chain creation reads enabled candidates across pages and swaps client order without touching nodes', async ({ page }) => {
  const chain = chainFixture()
  let body: typeof chain.chain | undefined
  const nodeRequests: string[] = []
  await mock(page, async (route, path) => {
    if (path === '/nodes') {
      nodeRequests.push(route.request().method())
      const url = new URL(route.request().url()); expect(url.searchParams.get('enabled')).toBe('true')
      await respond(route, url.searchParams.has('cursor') ? [exit] : [first, disabled], 200, { page: { limit: 200, ...(url.searchParams.has('cursor') ? {} : { next_cursor: 'fixture-page-2' }) } }); return true
    }
    if (path === '/chains' && route.request().method() === 'POST') { body = route.request().postDataJSON(); expect(route.request().headers()['x-csrf-token']).toBe(identity.csrf_token); await respond(route, chain, 201); return true }
    if (path === `/chains/${chain.metadata.resource_id}`) { await respond(route, chain); return true }
  })
  await page.goto('/chains/new')
  await expect(page.getByRole('heading', { name: '创建链路', exact: true })).toBeVisible()
  await page.getByLabel('链路名称', { exact: true }).fill('Ordered fixture')
  await expect(page.getByLabel('第一跳节点').locator('option')).toHaveCount(3)
  await expect(page.getByLabel('第一跳节点').locator('option', { hasText: disabled.metadata.name })).toHaveCount(0)
  await page.getByLabel('第一跳节点').selectOption(first.metadata.resource_id)
  await page.getByLabel('最终出口节点').selectOption(exit.metadata.resource_id)
  await page.getByRole('button', { name: '交换第一跳与出口' }).click()
  await expect(page.getByLabel('第一跳节点')).toHaveValue(exit.metadata.resource_id)
  await expect(page.getByLabel('链路顺序')).toContainText('客户端')
  await page.getByRole('button', { name: '创建链路', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Chain fixture', exact: true })).toBeVisible()
  expect(body?.hops).toEqual([{ node_id: exit.metadata.resource_id }, { node_id: first.metadata.resource_id }])
  expect(body?.failure_policy).toBe('fail_closed')
  expect(nodeRequests).toEqual(['GET', 'GET'])
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0])
})

test('policy editor offers four strategies, constrains default member and sends only contract health fields', async ({ page }) => {
  const chain = chainFixture(), group = groupFixture()
  let body: Record<string, any> | undefined
  await mock(page, async (route, path) => {
    if (path === '/chains') { await respond(route, [chain], 200, { page: { limit: 200 } }); return true }
    if (path === '/policy-groups' && route.request().method() === 'POST') { body = route.request().postDataJSON(); await respond(route, group, 201); return true }
    if (path === `/policy-groups/${group.metadata.resource_id}`) { await respond(route, group); return true }
  })
  await page.goto('/policy-groups/new')
  await page.getByLabel('策略组名称', { exact: true }).fill('Policy fixture')
  await expect(page.getByRole('combobox', { name: '策略', exact: true }).locator('option')).toHaveCount(4)
  await expect(page.getByText('结构保存 ≠ 内核 / 客户端兼容性验证')).toBeVisible()
  await page.getByRole('combobox', { name: '策略', exact: true }).selectOption('round_robin')
  await page.getByLabel('添加节点或链路').selectOption(`node:${first.metadata.resource_id}`)
  await page.getByRole('button', { name: '添加成员', exact: true }).click()
  await page.getByLabel('添加节点或链路').selectOption(`chain:${chain.metadata.resource_id}`)
  await page.getByRole('button', { name: '添加成员', exact: true }).click()
  await page.getByRole('combobox', { name: '默认成员', exact: true }).selectOption(`node:${first.metadata.resource_id}`)
  await page.getByRole('button', { name: '移除成员 1', exact: true }).click()
  await expect(page.getByRole('combobox', { name: '默认成员', exact: true })).toHaveValue('')
  await page.getByRole('combobox', { name: '默认成员', exact: true }).selectOption(`chain:${chain.metadata.resource_id}`)
  await page.getByLabel('启用客户端健康检查', { exact: true }).check()
  await page.getByLabel('检查地址（HTTPS）', { exact: true }).fill('https://example.com/check')
  await page.getByRole('button', { name: '创建策略组', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Group fixture', exact: true })).toBeVisible()
  expect(body?.policy_group).toMatchObject({ strategy: 'round_robin', members: [member('chain', chain.metadata.resource_id)], default_member: member('chain', chain.metadata.resource_id), on_unavailable: 'fail_closed', health_check: { enabled: true, url: 'https://example.com/check', interval_ms: 300000, timeout_ms: 5000, tolerance_ms: 50 } })
  expect(Object.keys(body!.policy_group.health_check).sort()).toEqual(['enabled', 'interval_ms', 'timeout_ms', 'tolerance_ms', 'url'])
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0])
})

for (const kind of ['chain', 'policy_group'] as const) {
  const label = kind === 'chain' ? '链路' : '策略组'
  const collection = kind === 'chain' ? '/chains' : '/policy-groups'
  test(`${kind} 412 preserves draft and retries only after comparing a fresh ETag`, async ({ page }) => {
    const saved = kind === 'chain' ? chainFixture() : groupFixture()
    const requests: { etag: string; body: { name: string } }[] = []
    await mock(page, async (route, path) => {
      if (path !== `${collection}/${saved.metadata.resource_id}`) return
      if (route.request().method() === 'PATCH') {
        requests.push({ etag: route.request().headers()['if-match'], body: route.request().postDataJSON() })
        if (requests.length === 1) await fail(route, 412, 'REVISION_MISMATCH')
        else { saved.metadata.name = requests[1].body.name; saved.metadata.revision = '3'; await respond(route, saved) }
      } else { if (requests.length === 1) { saved.metadata.name = 'Concurrent name'; saved.metadata.revision = '2' }; await respond(route, saved, 200, {}, `"r${saved.metadata.revision}"`) }
      return true
    })
    await page.goto(`${collection}/${saved.metadata.resource_id}/edit`)
    await page.getByLabel(`${label}名称`, { exact: true }).fill('Retained draft')
    await page.getByRole('button', { name: '保存新修订', exact: true }).click()
    await expect(page.getByRole('heading', { name: '修订冲突 · 草稿已保留' })).toBeVisible()
    await expect(page.getByLabel(`${label}名称`, { exact: true })).toHaveValue('Retained draft')
    await expect(page.getByRole('button', { name: '保存新修订', exact: true })).toBeDisabled()
    await page.getByRole('button', { name: '加载最新版本进行比较' }).click()
    await expect(page.getByRole('heading', { name: '服务器最新 · r2' })).toBeVisible()
    await page.getByLabel('已比较差异，确认基于最新修订继续编辑').check()
    await page.getByRole('button', { name: '保留草稿，采用最新修订号' }).click()
    await page.getByRole('button', { name: '保存新修订', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Retained draft', exact: true })).toBeVisible()
    expect(requests).toEqual([{ etag: '"r1"', body: { name: 'Retained draft' } }, { etag: '"r2"', body: { name: 'Retained draft' } }])
  })

  test(`${kind} list filters and pagination retain contract query and deletion uses current revision`, async ({ page }) => {
    const saved = kind === 'chain' ? chainFixture() : groupFixture()
    const queries: URLSearchParams[] = []
    let deleted = false
    await mock(page, async (route, path) => {
      if (path === collection) { const url = new URL(route.request().url()); queries.push(url.searchParams); await respond(route, deleted ? [] : [saved], 200, { page: { limit: 50, ...(url.searchParams.has('cursor') ? {} : { next_cursor: 'page2' }) } }); return true }
      if (path === `${collection}/${saved.metadata.resource_id}`) {
        if (route.request().method() === 'DELETE') { expect(route.request().headers()['if-match']).toBe('"r1"'); deleted = true; await respond(route, { resource_id: saved.metadata.resource_id, revision: '2', status: 'deleted' }) }
        else await respond(route, saved)
        return true
      }
    })
    await page.goto(collection)
    await page.getByRole('button', { name: '下一页', exact: true }).click()
    await expect(page.getByText('第 2 页 · 本页 1 条')).toBeVisible()
    await page.getByLabel('标签', { exact: true }).fill('fixture')
    await page.getByRole('button', { name: '筛选', exact: true }).click()
    await expect(page.getByText('第 1 页 · 本页 1 条')).toBeVisible()
    expect(queries[1].get('cursor')).toBe('page2')
    expect(queries[2].get('tag')).toBe('fixture')
    expect(queries[2].has('cursor')).toBe(false)
    await page.getByRole('link', { name: saved.metadata.name, exact: true }).click()
    await page.getByRole('button', { name: `删除${label}`, exact: true }).click()
    await page.getByRole('button', { name: '确认删除', exact: true }).click()
    await expect(page.getByRole('heading', { name: `${label}管理`, exact: true })).toBeVisible()
    expect(deleted).toBe(true)
  })
}
