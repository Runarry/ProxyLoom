import { test, expect, type APIRequestContext, type BrowserContext, type Page } from '@playwright/test'
import { randomUUID } from 'node:crypto'

const username = process.env.PROXYLOOM_E2E_USERNAME!
const administratorPassword = process.env.PROXYLOOM_E2E_PASSWORD!
const baseURL = process.env.PROXYLOOM_E2E_BASE_URL!
const prefix = `orchestration-${randomUUID().slice(0, 8)}`
type Metadata = { resource_id: string; revision: string; security_epoch: string; name: string }
type Resource<T extends object> = { metadata: Metadata } & T
type Session = { data: { csrf_token: string } }
let cookies: Awaited<ReturnType<BrowserContext['cookies']>> = []
let nodes: Array<Resource<object>> = []
let seedChain: Resource<object>

async function login(page: Page) {
  await page.goto('/login')
  await page.locator('[name="username"]').fill(username)
  await page.locator('[name="password"]').fill(administratorPassword)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '节点库', exact: true })).toBeVisible()
}
async function mutationHeaders(request: APIRequestContext) {
  const me = await request.get('/api/v1/auth/me')
  expect(me.status()).toBe(200)
  const session = await me.json() as Session
  return { Origin: new URL(baseURL).origin, 'X-CSRF-Token': session.data.csrf_token, 'Content-Type': 'application/json' }
}
async function createNode(request: APIRequestContext, name: string, host: string) {
  const response = await request.post('/api/v1/nodes', { headers: await mutationHeaders(request), data: {
    name, tags: ['orchestration-browser'], enabled: true,
    node: { schema_version: 1, protocol: 'http', endpoint: { host, port: 8080 }, auth: { kind: 'none' },
      transport: { kind: 'native_tcp' }, security: { mode: 'none' }, features: {}, extensions: {} },
  } })
  expect(response.status()).toBe(201)
  return (await response.json()).data as Resource<object>
}
async function open(page: Page, path: string) { await page.context().addCookies(cookies); await page.goto(path) }
async function read<T>(page: Page, path: string) {
  const response = await page.request.get(`/api/v1${path}`)
  expect(response.status()).toBe(200)
  return (await response.json()).data as T
}

test.beforeAll(async ({ browser }) => {
  const context = await browser.newContext({ baseURL }), page = await context.newPage()
  try {
    await login(page)
    nodes = [
      await createNode(page.request, `${prefix}-first`, 'first.example.invalid'),
      await createNode(page.request, `${prefix}-exit`, 'exit.example.invalid'),
      await createNode(page.request, `${prefix}-alternate`, 'alternate.example.invalid'),
    ]
    const chainResponse = await page.request.post('/api/v1/chains', { headers: await mutationHeaders(page.request), data: {
      name: `${prefix}-policy-chain`, hops: [{ node_id: nodes[0].metadata.resource_id }, { node_id: nodes[1].metadata.resource_id }], failure_policy: 'fail_closed',
    } })
    expect(chainResponse.status()).toBe(201)
    seedChain = (await chainResponse.json()).data
    cookies = await context.cookies()
  } finally { await context.close() }
})
test.afterEach(async ({ page }) => {
  cookies = await page.context().cookies()
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0])
})
test.afterAll(async ({ browser }) => {
  if (!cookies.length) return
  const context = await browser.newContext({ baseURL })
  try {
    await context.addCookies(cookies)
    const me = await context.request.get('/api/v1/auth/me')
    if (me.status() === 200) {
      const session = await me.json() as Session
      expect((await context.request.post('/api/v1/auth/logout', { headers: { Origin: new URL(baseURL).origin, 'X-CSRF-Token': session.data.csrf_token }, data: {} })).status()).toBe(200)
    }
  } finally { cookies = []; await context.close() }
})

test('chain swap persists client order and leaves referenced nodes unchanged', async ({ page }) => {
  await open(page, '/chains/new')
  await page.getByLabel('链路名称', { exact: true }).fill(`${prefix}-chain`)
  await page.getByRole('combobox', { name: '第一跳节点', exact: true }).selectOption(nodes[0].metadata.resource_id)
  await page.getByRole('combobox', { name: '最终出口节点', exact: true }).selectOption(nodes[1].metadata.resource_id)
  await page.getByRole('button', { name: '创建链路', exact: true }).click()
  await expect(page.getByRole('heading', { name: `${prefix}-chain`, exact: true })).toBeVisible()
  const id = new URL(page.url()).pathname.split('/').at(-1)!
  let chain = await read<Resource<{ chain: { hops: Array<{ node_id: string }> } }>>(page, `/chains/${id}`)
  expect(chain.chain.hops.map(hop => hop.node_id)).toEqual([nodes[0].metadata.resource_id, nodes[1].metadata.resource_id])
  await page.getByRole('link', { name: '编辑链路', exact: true }).click()
  await page.getByRole('button', { name: '交换第一跳与出口', exact: true }).click()
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: `${prefix}-chain`, exact: true })).toBeVisible()
  chain = await read(page, `/chains/${id}`)
  expect(chain.metadata.revision).toBe('2')
  expect(chain.chain.hops.map(hop => hop.node_id)).toEqual([nodes[1].metadata.resource_id, nodes[0].metadata.resource_id])
  for (const original of nodes) {
    const node = await read<Resource<object>>(page, `/nodes/${original.metadata.resource_id}`)
    expect(node.metadata).toMatchObject({ revision: '1', security_epoch: '1' })
  }
})

test('policy group preserves round robin semantics and 412 keeps its draft until comparison', async ({ page }) => {
  await open(page, '/policy-groups/new')
  await page.getByLabel('策略组名称', { exact: true }).fill(`${prefix}-policy`)
  await page.getByRole('combobox', { name: '策略', exact: true }).selectOption('round_robin')
  await page.getByRole('combobox', { name: '添加节点或链路', exact: true }).selectOption(`node:${nodes[2].metadata.resource_id}`)
  await page.getByRole('button', { name: '添加成员', exact: true }).click()
  await page.getByRole('combobox', { name: '添加节点或链路', exact: true }).selectOption(`chain:${seedChain.metadata.resource_id}`)
  await page.getByRole('button', { name: '添加成员', exact: true }).click()
  await page.getByRole('combobox', { name: '默认成员', exact: true }).selectOption(`chain:${seedChain.metadata.resource_id}`)
  await page.getByRole('button', { name: '创建策略组', exact: true }).click()
  await expect(page.getByRole('heading', { name: `${prefix}-policy`, exact: true })).toBeVisible()
  const id = new URL(page.url()).pathname.split('/').at(-1)!
  let policy = await read<Resource<{ policy_group: { strategy: string; members: object[]; default_member: { resource_id: string } } }>>(page, `/policy-groups/${id}`)
  expect(policy.policy_group.strategy).toBe('round_robin')
  expect(policy.policy_group.members).toHaveLength(2)
  expect(policy.policy_group.default_member.resource_id).toBe(seedChain.metadata.resource_id)

  await page.getByRole('link', { name: '编辑策略组', exact: true }).click()
  const retained = `${prefix}-policy-retained`
  await page.getByLabel('策略组名称', { exact: true }).fill(retained)
  const concurrent = await page.request.patch(`/api/v1/policy-groups/${id}`, {
    headers: { ...(await mutationHeaders(page.request)), 'If-Match': '"r1"' }, data: { name: `${prefix}-policy-concurrent` },
  })
  expect(concurrent.status()).toBe(200)
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: '修订冲突 · 草稿已保留', exact: true })).toBeVisible()
  await expect(page.getByLabel('策略组名称', { exact: true })).toHaveValue(retained)
  await page.getByRole('button', { name: '加载最新版本进行比较', exact: true }).click()
  await expect(page.getByRole('heading', { name: '服务器最新 · r2', exact: true })).toBeVisible()
  await page.getByLabel('已比较差异，确认基于最新修订继续编辑', { exact: true }).check()
  await page.getByRole('button', { name: '保留草稿，采用最新修订号', exact: true }).click()
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: retained, exact: true })).toBeVisible()
  policy = await read(page, `/policy-groups/${id}`)
  expect(policy.metadata).toMatchObject({ name: retained, revision: '3' })
  expect(policy.policy_group.strategy).toBe('round_robin')
})

test('rule text locates its CIDR error and ordered routing persists after edit', async ({ page }) => {
  await open(page, '/rule-sets/new')
  await page.getByLabel('规则集名称', { exact: true }).fill(`${prefix}-rules`)
  await page.getByLabel('规则文本', { exact: true }).fill('DOMAIN,example.com\nIP-CIDR,192.0.2.1/24')
  await page.getByRole('button', { name: '检查并预览规范化条目', exact: true }).click()
  await expect(page.getByRole('button', { name: '第 2 行', exact: true })).toBeVisible()
  await page.getByLabel('规则文本', { exact: true }).fill('# normalized\nDOMAIN-SUFFIX,Example.COM.\nIP-CIDR,192.0.2.0/24')
  await page.getByRole('button', { name: '检查并预览规范化条目', exact: true }).click()
  await expect(page.getByText('文本检查通过，共 2 条。', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '创建规则集', exact: true }).click()
  await expect(page.getByRole('heading', { name: `${prefix}-rules`, exact: true })).toBeVisible()
  const ruleSetID = new URL(page.url()).pathname.split('/').at(-1)!
  const ruleSet = await read<Resource<{ rule_set: { entries: object[]; content_hash: string } }>>(page, `/rule-sets/${ruleSetID}`)
  expect(ruleSet.rule_set.entries).toEqual([
    { kind: 'domain', domain: 'example.com', match: 'suffix' },
    { kind: 'cidr', cidr: '192.0.2.0/24' },
  ])
  expect(ruleSet.rule_set.content_hash).toMatch(/^[0-9a-f]{64}$/)

  const invalid = await page.request.post('/api/v1/rule-sets', { headers: await mutationHeaders(page.request), data: {
    name: `${prefix}-invalid-rules`, rule_set: { schema_version: 1, format: 'domain_cidr_text', entries: [
      { kind: 'domain', domain: 'valid.example', match: 'exact' }, { kind: 'cidr', cidr: '192.0.2.1/24' },
    ] },
  } })
  expect(invalid.status()).toBe(422)
  const invalidBody = await invalid.json()
  expect(invalidBody.error.details.some((detail: { field_path?: string }) => detail.field_path?.startsWith('/rule_set/entries/1'))).toBe(true)

  await open(page, '/routing/new')
  await page.getByLabel('路由配置名称', { exact: true }).fill(`${prefix}-routing`)
  await page.getByRole('button', { name: '添加路由规则', exact: true }).click()
  await page.getByRole('button', { name: '添加路由规则', exact: true }).click()
  const first = page.getByRole('group', { name: '路由规则 1', exact: true })
  const second = page.getByRole('group', { name: '路由规则 2', exact: true })
  await first.getByLabel('备注', { exact: true }).fill('first-before-move')
  await first.getByLabel('精确域名（每行一个）', { exact: true }).fill('first.example.com')
  await first.getByLabel(`${prefix}-rules · ${ruleSetID}`, { exact: true }).check()
  await second.getByLabel('备注', { exact: true }).fill('second-before-move')
  await second.getByLabel('域名后缀（每行一个）', { exact: true }).fill('second.example.com')
  await second.getByRole('combobox', { name: '命中动作', exact: true }).selectOption('builtin:direct')
  await page.getByLabel('路由配置名称', { exact: true }).click()
  await page.getByRole('button', { name: '上移路由规则 2', exact: true }).click()
  await page.getByRole('button', { name: '创建路由配置', exact: true }).click()
  await expect(page.getByRole('heading', { name: `${prefix}-routing`, exact: true })).toBeVisible()
  const routingID = new URL(page.url()).pathname.split('/').at(-1)!
  let routing = await read<Resource<{ routing_profile: { rules: Array<{ comment: string; action: object; match: { rule_set_ids?: string[] } }> } }>>(page, `/routing-profiles/${routingID}`)
  expect(routing.routing_profile.rules.map(rule => rule.comment)).toEqual(['second-before-move', 'first-before-move'])
  expect(routing.routing_profile.rules[0].action).toEqual({ type: 'builtin', builtin: 'direct' })
  expect(routing.routing_profile.rules[1].match.rule_set_ids).toEqual([ruleSetID])
  await page.getByRole('link', { name: '编辑路由配置', exact: true }).click()
  await page.getByLabel('路由配置名称', { exact: true }).fill(`${prefix}-routing-edited`)
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: `${prefix}-routing-edited`, exact: true })).toBeVisible()
  routing = await read(page, `/routing-profiles/${routingID}`)
  expect(routing.metadata.revision).toBe('2')
  expect(routing.routing_profile.rules.map(rule => rule.comment)).toEqual(['second-before-move', 'first-before-move'])
})

test('DNS persists explicit resolvers, rejects a server-side cycle, and exposes five read-only presets', async ({ page }) => {
  await open(page, '/dns/new')
  await page.getByLabel('DNS 配置名称', { exact: true }).fill(`${prefix}-dns`)
  await page.getByRole('button', { name: '添加主解析器', exact: true }).click()
  const second = page.getByRole('group', { name: '主解析器 2', exact: true })
  await second.getByRole('combobox', { name: '解析类型', exact: true }).selectOption('https')
  await second.getByLabel('HTTPS 解析地址', { exact: true }).fill('https://dns.example.invalid/dns-query')
  await second.getByRole('combobox', { name: '引导解析器', exact: true }).selectOption('bootstrap')
  await second.getByRole('combobox', { name: 'DNS 出站动作', exact: true }).selectOption('builtin:direct')
  await page.getByRole('button', { name: '创建DNS 配置', exact: true }).click()
  await expect(page.getByRole('heading', { name: `${prefix}-dns`, exact: true })).toBeVisible()
  const dnsID = new URL(page.url()).pathname.split('/').at(-1)!
  let dns = await read<Resource<{ dns_profile: { bootstrap: object[]; resolvers: Array<{ kind: string }>; final_resolver: string } }>>(page, `/dns-profiles/${dnsID}`)
  expect(dns.dns_profile.bootstrap).toHaveLength(1)
  expect(dns.dns_profile.resolvers.map(resolver => resolver.kind)).toEqual(['local', 'https'])
  expect(dns.dns_profile.final_resolver).toBe('local')
  await page.getByRole('link', { name: '编辑DNS 配置', exact: true }).click()
  await page.getByLabel('DNS 配置名称', { exact: true }).fill(`${prefix}-dns-edited`)
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: `${prefix}-dns-edited`, exact: true })).toBeVisible()
  dns = await read(page, `/dns-profiles/${dnsID}`)
  expect(dns.metadata.revision).toBe('2')

  const direct = { type: 'builtin', builtin: 'direct' }
  const cyclic = await page.request.post('/api/v1/dns-profiles', { headers: await mutationHeaders(page.request), data: {
    name: `${prefix}-cyclic-dns`, dns_profile: { schema_version: 1,
      bootstrap: [{ resolver_id: 'bootstrap', kind: 'local' }],
      resolvers: [
        { resolver_id: 'one', kind: 'https', url: 'https://one.example.invalid/dns-query', bootstrap_resolver_id: 'two', outbound: direct },
        { resolver_id: 'two', kind: 'https', url: 'https://two.example.invalid/dns-query', bootstrap_resolver_id: 'one', outbound: direct },
      ], rules: [], final_resolver: 'one' },
  } })
  expect(cyclic.status()).toBe(422)
  const cyclicBody = await cyclic.json()
  const cyclePaths = cyclicBody.error.details.map((detail: { field_path?: string }) => detail.field_path)
  expect(cyclePaths).toEqual(expect.arrayContaining([
    '/dns_profile/resolvers/0/bootstrap_resolver_id', '/dns_profile/resolvers/1/bootstrap_resolver_id',
  ]))
  expect(JSON.stringify(cyclicBody)).not.toContain('one.example.invalid')

  await open(page, '/client-presets')
  await expect(page.getByRole('heading', { name: '客户端预设', exact: true })).toBeVisible()
  await expect(page.locator('.preset-grid > section')).toHaveCount(5)
  await expect(page.getByText('系统预设 · 只读', { exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: /创建|编辑/ })).toHaveCount(0)
})
