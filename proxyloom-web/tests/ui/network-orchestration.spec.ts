import { test, expect, type Page, type Route } from '@playwright/test'
import type { Schema } from '../../src/api/client'

const randomUUID = () => globalThis.crypto.randomUUID()
const identity = { user_id: randomUUID(), username: 'fixture', role: 'administrator', session_expires_at: '2099-01-01T00:00:00Z', csrf_token: 'x'.repeat(43) }
const metadata = (name: string, kind: Schema<'ResourceKind'>): Schema<'ResourceMetadata'> => ({ resource_id: randomUUID(), scope_id: randomUUID(), kind, schema_version: 1, security_epoch: '1', name, tags: [], enabled: true, revision: '1' })
const node = { metadata: metadata('Node fixture', 'node') }, chain = { metadata: metadata('Chain fixture', 'chain') }, group = { metadata: metadata('Group fixture', 'policy_group') }
const ref = (resource: typeof node) => ({ type: 'resource_ref', kind: resource.metadata.kind, resource_id: resource.metadata.resource_id })
const reject = { type: 'builtin' as const, builtin: 'reject' as const }
const rulesFixture = (): Schema<'RuleSetResource'> => ({ metadata: metadata('Rules fixture', 'rule_set'), rule_set: { schema_version: 1, format: 'domain_cidr_text', entries: [{ kind: 'domain', domain: 'example.com', match: 'exact' }], content_hash: '0'.repeat(64) } })
const routingFixture = (): Schema<'RoutingProfileResource'> => ({ metadata: metadata('Routing fixture', 'routing_profile'), routing_profile: { schema_version: 1, domain_resolution_mode: 'preserve_domain', final: reject, rules: [{ enabled: true, comment: '', match: { domain_suffix: ['example.com'] }, action: reject }] } })
const dnsFixture = (): Schema<'DNSProfileResource'> => ({ metadata: metadata('DNS fixture', 'dns_profile'), dns_profile: { schema_version: 1, bootstrap: [{ resolver_id: 'bootstrap', kind: 'local' }], resolvers: [{ resolver_id: 'local', kind: 'local' }], rules: [], final_resolver: 'local' }, diagnostics: [] })
const respond = (route: Route, data: unknown, status = 200, extra = {}, etag = '"r1"') => route.fulfill({ status, json: { request_id: 'ui-network', data, ...extra }, headers: { ETag: etag, 'Cache-Control': 'no-store' } })
const fail = (route: Route, code: string, details: { field_path: string }[] = [], status = 422) => route.fulfill({ status, json: { request_id: 'ui-network', error: { code, message: 'Synthetic fixed error', details } } })
async function mock(page: Page, handler: (route: Route, path: string) => Promise<boolean | void>) {
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    if (path === '/auth/me') return respond(route, identity)
    if (await handler(route, path)) return
    const collections: Record<string, unknown[]> = { '/nodes': [node], '/chains': [chain], '/policy-groups': [group], '/rule-sets': [rulesFixture()], '/routing-profiles': [], '/dns-profiles': [] }
    if (path in collections) return respond(route, collections[path], 200, { page: { limit: 200 } })
    return fail(route, 'RESOURCE_NOT_FOUND', [], 404)
  })
}

test('routing saves typed targets and AND/OR conditions in keyboard reordered rule order', async ({ page }) => {
  const saved = routingFixture()
  let body: Schema<'RoutingProfileCreateRequest'> | undefined
  await mock(page, async (route, path) => {
    if (path === '/routing-profiles' && route.request().method() === 'POST') { body = route.request().postDataJSON(); expect(route.request().headers()['x-csrf-token']).toBe(identity.csrf_token); await respond(route, saved, 201); return true }
    if (path === `/routing-profiles/${saved.metadata.resource_id}`) { await respond(route, saved); return true }
  })
  await page.goto('/routing/new')
  await page.getByLabel('路由配置名称', { exact: true }).fill('Ordered routing')
  const final = page.getByRole('combobox', { name: '最终动作', exact: true })
  for (const resource of [node, chain, group]) await expect(final.locator(`option[value="${resource.metadata.kind}:${resource.metadata.resource_id}"]`)).toHaveCount(1)
  await final.selectOption(`policy_group:${group.metadata.resource_id}`)
  await page.getByRole('button', { name: '添加路由规则' }).click()
  let rule = page.getByRole('group', { name: '路由规则 1', exact: true })
  await rule.getByLabel('备注', { exact: true }).fill('domain rule')
  await rule.getByLabel('域名后缀（每行一个）').fill('EXAMPLE.COM\nexample.org')
  await rule.getByLabel('TCP', { exact: true }).check()
  await rule.getByRole('button', { name: '添加端口范围' }).click()
  await rule.getByRole('combobox', { name: '命中动作', exact: true }).selectOption(`chain:${chain.metadata.resource_id}`)
  await page.getByRole('button', { name: '添加路由规则' }).click()
  rule = page.getByRole('group', { name: '路由规则 2', exact: true })
  await rule.getByLabel('备注', { exact: true }).fill('CIDR rule')
  await rule.getByLabel('目标 IP 网段（每行一个 CIDR）').fill('192.0.2.0/24')
  await rule.getByRole('combobox', { name: '命中动作', exact: true }).selectOption('builtin:direct')
  await page.getByRole('button', { name: '上移路由规则 2' }).focus()
  await page.keyboard.press('Enter')
  await expect(page.getByRole('group', { name: '路由规则 1', exact: true }).getByLabel('备注', { exact: true })).toHaveValue('CIDR rule')
  await page.getByRole('button', { name: '创建路由配置', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Routing fixture', exact: true })).toBeVisible()
  expect(body?.routing_profile).toEqual({ schema_version: 1, domain_resolution_mode: 'preserve_domain', final: ref(group), rules: [
    { enabled: true, comment: 'CIDR rule', match: { ip_cidrs: ['192.0.2.0/24'] }, action: { type: 'builtin', builtin: 'direct' } },
    { enabled: true, comment: 'domain rule', match: { domain_suffix: ['example.com', 'example.org'], network: ['tcp'], destination_ports: [{ from: 443, to: 443 }] }, action: ref(chain) },
  ] })
})

test('DNS renames update dependencies, changed types omit hidden fields and server errors focus the nested outbound', async ({ page }) => {
  const saved = dnsFixture()
  let body: Schema<'DNSProfileCreateRequest'> | undefined, writes = 0
  await mock(page, async (route, path) => {
    if (path === '/dns-profiles' && route.request().method() === 'POST') {
      body = route.request().postDataJSON(); writes++
      if (writes === 1) await fail(route, 'DNS_CYCLE', [{ field_path: '/dns_profile/resolvers/1/outbound/resource_id' }])
      else await respond(route, saved, 201)
      return true
    }
    if (path === `/dns-profiles/${saved.metadata.resource_id}`) { await respond(route, saved); return true }
  })
  await page.goto('/dns/new')
  await page.getByLabel('DNS 配置名称', { exact: true }).fill('Dependency DNS')
  await page.getByRole('combobox', { name: '引导类型', exact: true }).selectOption('udp')
  await page.getByLabel('引导 IP 地址').fill('192.0.2.53')
  await page.getByRole('button', { name: '添加主解析器' }).click()
  const resolver = page.getByRole('group', { name: '主解析器 2', exact: true })
  await resolver.getByRole('combobox', { name: '解析类型', exact: true }).selectOption('https')
  await resolver.getByLabel('HTTPS 解析地址').fill('https://example.com/dns-query')
  await resolver.getByRole('combobox', { name: 'DNS 出站动作', exact: true }).selectOption(`chain:${chain.metadata.resource_id}`)
  await page.getByLabel('未命中规则时使用').selectOption('resolver_1')
  await page.getByRole('button', { name: '添加 DNS 规则' }).click()
  const rule = page.getByRole('group', { name: 'DNS 规则 1', exact: true })
  await rule.getByLabel('域名后缀（每行一个）').fill('EXAMPLE.COM')
  await page.getByLabel('引导解析器 ID', { exact: true }).fill('seed')
  await page.getByLabel('引导解析器 ID', { exact: true }).press('Tab')
  await resolver.getByLabel('解析器 ID', { exact: true }).fill('remote')
  await resolver.getByLabel('解析器 ID', { exact: true }).press('Tab')
  await expect(resolver.getByRole('combobox', { name: '引导解析器', exact: true })).toHaveValue('seed')
  await expect(page.getByLabel('未命中规则时使用')).toHaveValue('remote')
  await expect(rule.getByLabel('命中解析器')).toHaveValue('remote')
  await expect(page.getByRole('button', { name: '移除主解析器 2', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: '创建DNS 配置', exact: true }).click()
  await expect(page.getByText('DNS_CYCLE · HTTP 422', { exact: false })).toBeVisible()
  await page.getByRole('button', { name: '检查字段 /dns_profile/resolvers/1/outbound/resource_id', exact: true }).click()
  await expect(resolver.getByRole('combobox', { name: 'DNS 出站动作', exact: true })).toBeFocused()
  expect(body?.dns_profile).toMatchObject({ bootstrap: [{ resolver_id: 'seed', kind: 'udp', address: '192.0.2.53', port: 53 }], resolvers: [{ resolver_id: 'local', kind: 'local' }, { resolver_id: 'remote', kind: 'https', bootstrap_resolver_id: 'seed', outbound: ref(chain) }], final_resolver: 'remote', rules: [{ resolver_id: 'remote', match: { domain_suffix: ['example.com'] } }] })
  await resolver.getByRole('combobox', { name: '解析类型', exact: true }).selectOption('local')
  await expect(resolver.getByLabel('HTTPS 解析地址')).toHaveCount(0)
  await page.getByRole('button', { name: '创建DNS 配置', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'DNS fixture', exact: true })).toBeVisible()
  expect(body?.dns_profile.resolvers[1]).toEqual({ resolver_id: 'remote', kind: 'local' })
})

test('rule text rejects native grammar, sends normalized entries only and maps server array errors to original lines', async ({ page }) => {
  const saved = rulesFixture()
  let body: Schema<'RuleSetCreateRequest'> | undefined, writes = 0
  await mock(page, async (route, path) => {
    if (path === '/rule-sets' && route.request().method() === 'POST') {
      body = route.request().postDataJSON(); writes++
      if (writes === 1) await fail(route, 'VALIDATION_FAILED', [{ field_path: '/rule_set/entries/1/domain' }])
      else await respond(route, saved, 201)
      return true
    }
    if (path === `/rule-sets/${saved.metadata.resource_id}`) { await respond(route, saved); return true }
  })
  await page.goto('/rule-sets/new')
  await page.getByLabel('规则集名称', { exact: true }).fill('Text fixture')
  const text = page.getByLabel('规则文本', { exact: true })
  await text.fill('DOMAIN,example.com,DIRECT')
  await page.getByRole('button', { name: '检查并预览规范化条目' }).click()
  await expect(page.getByRole('button', { name: '第 1 行', exact: true })).toBeVisible()
  expect(writes).toBe(0)
  await text.fill('# fixture comment\n\nDOMAIN,EXAMPLE.COM\nDOMAIN-SUFFIX,例子.COM\nIP-CIDR6,2001:DB8::/32')
  await page.getByRole('button', { name: '检查并预览规范化条目' }).click()
  await expect(page.getByText('文本检查通过，共 3 条。')).toBeVisible()
  await page.getByRole('button', { name: '创建规则集', exact: true }).click()
  await page.getByRole('button', { name: '第 4 行', exact: true }).click()
  await expect(text).toBeFocused()
  expect(await text.evaluate(element => { const control = element as HTMLTextAreaElement; return control.value.slice(control.selectionStart, control.selectionEnd) })).toBe('DOMAIN-SUFFIX,例子.COM')
  expect(body?.rule_set).toEqual({ schema_version: 1, format: 'domain_cidr_text', entries: [{ kind: 'domain', domain: 'example.com', match: 'exact' }, { kind: 'domain', domain: 'xn--fsqu00a.com', match: 'suffix' }, { kind: 'cidr', cidr: '2001:db8::/32' }] })
  expect(Object.keys(body!).sort()).toEqual(['enabled', 'name', 'rule_set', 'tags'])
  await page.getByRole('button', { name: '创建规则集', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Rules fixture', exact: true })).toBeVisible()
})

for (const [apiPath, pagePath, label, fixture] of [
  ['/routing-profiles', '/routing', '路由配置', routingFixture], ['/dns-profiles', '/dns', 'DNS 配置', dnsFixture], ['/rule-sets', '/rule-sets', '规则集', rulesFixture],
] as const) {
  test(`${apiPath} 412 retains the draft and can adopt the latest server version without a reactive clone error`, async ({ page }) => {
    const saved = fixture()
    const writes: { etag: string; body: unknown }[] = []
    await mock(page, async (route, path) => {
      if (path !== `${apiPath}/${saved.metadata.resource_id}`) return
      if (route.request().method() === 'PATCH') {
        writes.push({ etag: route.request().headers()['if-match'], body: route.request().postDataJSON() })
        if (writes.length === 1) await fail(route, 'REVISION_MISMATCH', [], 412)
        else { saved.metadata.name = 'Retained draft'; saved.metadata.revision = '3'; await respond(route, saved) }
      } else { if (writes.length === 1) { saved.metadata.name = 'Concurrent name'; saved.metadata.revision = '2' }; await respond(route, saved, 200, {}, `"r${saved.metadata.revision}"`) }
      return true
    })
    await page.goto(`${pagePath}/${saved.metadata.resource_id}/edit`)
    await page.getByLabel(`${label}名称`, { exact: true }).fill('Retained draft')
    await page.getByRole('button', { name: '保存新修订', exact: true }).click()
    await expect(page.getByRole('heading', { name: '修订冲突 · 草稿已保留' })).toBeVisible()
    await expect(page.getByLabel(`${label}名称`, { exact: true })).toHaveValue('Retained draft')
    await expect(page.getByRole('button', { name: '保存新修订', exact: true })).toBeDisabled()
    await page.getByRole('button', { name: '加载最新版本进行比较' }).click()
    await expect(page.getByRole('heading', { name: '服务器最新 · r2' })).toBeVisible()
    await page.getByRole('button', { name: '放弃草稿，采用最新版本' }).click()
    await expect(page.getByLabel(`${label}名称`, { exact: true })).toHaveValue('Concurrent name')
    await page.getByLabel(`${label}名称`, { exact: true }).fill('Retained draft')
    await page.getByRole('button', { name: '保存新修订', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Retained draft', exact: true })).toBeVisible()
    expect(writes).toEqual([{ etag: '"r1"', body: { name: 'Retained draft' } }, { etag: '"r2"', body: { name: 'Retained draft' } }])
    expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0])
  })
}

test('five immutable presets expose loopback control choices and keep unavailable validation results unverified', async ({ page }) => {
  const presets = ([['xray', 0], ['sing-box', 0], ['mihomo', 0], ['sing-box', 17812], ['mihomo', 17813]] as const).map(([core, port]): Schema<'ClientPresetResource'> => ({ metadata: metadata(`${core} fixture ${port}`, 'client_preset'), preset: { schema_version: 1, core_family: core, platform: 'linux', format: core === 'mihomo' ? 'mihomo_yaml' : core === 'sing-box' ? 'singbox_json' : 'xray_json', import_method: 'file', local_listener: { protocol: 'socks5', listen: '127.0.0.1', port: 1080 }, control_api: port ? { enabled: true, listen: '127.0.0.1', port } : { enabled: false }, dns_mode: 'profile', review_status: 'approved', reviewed_at: '2026-09-10T00:00:00Z' } }))
  const requests: { method: string; query: URLSearchParams }[] = []
  await mock(page, async (route, path) => {
    if (path !== '/client-presets') return
    const query = new URL(route.request().url()).searchParams
    requests.push({ method: route.request().method(), query })
    await respond(route, query.get('core_family') ? presets.filter(resource => resource.preset.core_family === query.get('core_family')) : presets, 200, { page: { limit: 50 } }); return true
  })
  await page.goto('/client-presets')
  await expect(page.locator('.preset-grid > section')).toHaveCount(5)
  await expect(page.getByText('系统预设 · 只读')).toBeVisible()
  await expect(page.getByText('已启用 · 127.0.0.1:17812', { exact: true })).toBeVisible()
  await expect(page.getByText('已启用 · 127.0.0.1:17813', { exact: true })).toBeVisible()
  await expect(page.getByText('已关闭', { exact: true })).toHaveCount(3)
  await expect(page.getByText('未验证（unverified）', { exact: false })).toBeVisible()
  await expect(page.getByRole('button', { name: /创建|编辑|删除/ })).toHaveCount(0)
  await page.getByRole('combobox', { name: '内核', exact: true }).selectOption('sing-box')
  await page.getByRole('combobox', { name: '平台', exact: true }).selectOption('linux')
  await page.getByRole('button', { name: '筛选', exact: true }).click()
  await expect(page.locator('.preset-grid > section')).toHaveCount(2)
  expect(requests.map(request => request.method)).toEqual(['GET', 'GET'])
  expect(requests[1].query.get('platform')).toBe('linux')
})
