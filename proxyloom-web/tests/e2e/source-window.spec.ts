import { test, expect, type BrowserContext, type Page } from '@playwright/test'
import { randomUUID } from 'node:crypto'
import { Buffer } from 'node:buffer'

const username = process.env.PROXYLOOM_E2E_USERNAME!
const administratorPassword = process.env.PROXYLOOM_E2E_PASSWORD!
const baseURL = process.env.PROXYLOOM_E2E_BASE_URL!
const fixtureURL = process.env.PROXYLOOM_E2E_SOURCE_FIXTURE_URL!
const prefix = `source-window-${randomUUID().slice(0, 8)}`
let cachedCookies: Awaited<ReturnType<BrowserContext['cookies']>> = []

test.beforeAll(() => {
  expect(fixtureURL).toMatch(/^http:\/\/127\.0\.0\.1:\d+$/)
})

async function login(page: Page) {
  if (cachedCookies.length) {
    await page.context().addCookies(cachedCookies)
    await page.goto('/sources')
    await expect(page.getByRole('heading', { name: '订阅来源', exact: true })).toBeVisible()
    return
  }
  await page.goto('/login')
  await page.locator('[name="username"]').fill(username)
  await page.locator('[name="password"]').fill(administratorPassword)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '节点库', exact: true })).toBeVisible()
  cachedCookies = await page.context().cookies()
}

async function mutationHeaders(page: Page) {
  const me = await page.request.get('/api/v1/auth/me')
  expect(me.status()).toBe(200)
  const session = await me.json()
  return { Origin: new URL(baseURL).origin, 'X-CSRF-Token': session.data.csrf_token, 'Content-Type': 'application/json' }
}

async function emptyStorage(page: Page) {
  expect(await page.evaluate(() => ({ local: localStorage.length, session: sessionStorage.length }))).toEqual({ local: 0, session: 0 })
}

async function createSource(page: Page, name: string, path: string, mode: 'manual' | 'safe_updates' = 'manual') {
  await page.goto('/sources/new')
  await page.getByLabel('来源名称', { exact: true }).fill(name)
  await page.getByRole('combobox', { name: '订阅格式', exact: true }).selectOption('uri_list')
  await page.getByLabel('来源地址新值', { exact: true }).fill(`${fixtureURL}${path}`)
  const commitMode = page.getByRole('combobox', { name: '变更提交方式', exact: true })
  await expect(commitMode).toHaveValue('manual')
  if (mode !== 'manual') await commitMode.selectOption(mode)
  const create = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/sources')
  await page.getByRole('button', { name: '创建来源', exact: true }).click()
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible()
  const request = await create
  const body = request.postDataJSON()
  expect(body.source.refresh_policy.commit_mode).toBe(mode)
  const id = new URL(page.url()).pathname.split('/').at(-1)!
  expect(id).toMatch(/^[0-9a-f-]{36}$/)
  return id
}

async function refreshAndOpenPreview(page: Page) {
  const currentLink = page.getByRole('link', { name: '查看最新来源预览', exact: true })
  const previous = await currentLink.count() ? await currentLink.getAttribute('href') : ''
  await page.getByRole('button', { name: '手动刷新', exact: true }).click()
  const link = page.getByRole('link', { name: '查看最新来源预览', exact: true })
  await expect.poll(async () => await link.count() ? await link.getAttribute('href') : '', { timeout: 30_000 }).not.toBe(previous)
  await expect(link).toBeVisible()
  await expect(page.getByText('刷新完成', { exact: true })).toBeVisible()
  await link.click()
  await expect(page.getByRole('heading', { name: '检查导入预览', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: '来源刷新预览', exact: true })).toBeVisible()
  return new URL(page.url()).pathname.split('/').at(-1)!
}

async function listNodes(page: Page, name: string) {
  const response = await page.request.get(`/api/v1/nodes?limit=50&q=${encodeURIComponent(name)}`)
  expect(response.status()).toBe(200)
  return (await response.json()).data as Array<any>
}

test.afterEach(async ({ page }) => {
  cachedCookies = await page.context().cookies()
})

test.afterAll(async ({ browser }) => {
  if (!cachedCookies.length) return
  const context = await browser.newContext({ baseURL })
  try {
    await context.addCookies(cachedCookies)
    const me = await context.request.get('/api/v1/auth/me')
    if (me.status() === 200) {
      const session = await me.json()
      const logout = await context.request.post('/api/v1/auth/logout', { headers: { Origin: new URL(baseURL).origin, 'X-CSRF-Token': session.data.csrf_token }, data: {} })
      expect(logout.status()).toBe(200)
    } else expect(me.status()).toBe(401)
  } finally {
    cachedCookies = []
    await context.close()
  }
})

test('manual source preview is isolated until selected commit, then binding preserves and restores overrides', async ({ page }) => {
  await login(page)
  const sourceName = `${prefix}-manual`
  const sourceID = await createSource(page, sourceName, '/manual/initial')
  await emptyStorage(page)
  await refreshAndOpenPreview(page)
  expect(await listNodes(page, 'Upstream Alpha')).toHaveLength(0)
  await expect(page.getByTestId('import-candidate')).toHaveCount(1)
  await expect(page.getByText('上游新增', { exact: true })).toBeVisible()
  await page.getByRole('combobox', { name: '第 1 项操作', exact: true }).selectOption('create')
  await page.getByLabel('我已检查诊断和匹配项，确认将这些选择一次性写入节点库').check()
  const commitRequest = page.waitForRequest(request => request.method() === 'POST' && /\/api\/v1\/imports\/[0-9a-f-]+\/commit$/.test(new URL(request.url()).pathname))
  await page.getByRole('button', { name: '原子提交 1 项', exact: true }).click()
  await expect(page.getByRole('heading', { name: '导入已完成', exact: true })).toBeVisible()
  const committed = await commitRequest
  const committedBody = committed.postDataJSON()
  expect(committedBody.source_revision).toBeTruthy()
  expect(committedBody.decisions).toHaveLength(1)
  expect(committedBody.decisions[0]).toMatchObject({ action: 'create' })
  expect(committed.headers()['if-match']).toBeTruthy()
  expect(committed.headers()['idempotency-key']).toBeTruthy()

  const created = await listNodes(page, 'Upstream Alpha')
  expect(created).toHaveLength(1)
  const nodeID = created[0].metadata.resource_id as string
  const boundRead = await page.request.get(`/api/v1/nodes/${nodeID}`)
  expect(boundRead.status()).toBe(200)
  const bound = (await boundRead.json()).data
  expect(bound.binding.source_resource_id).toBe(sourceID)
  expect(bound.node.endpoint.port).toBe(18080)

  const localName = `${prefix}-local-name`
  await page.goto(`/nodes/${nodeID}/edit`)
  await expect(page.getByText(/当前节点绑定来源/)).toBeVisible()
  await page.getByLabel('节点名称', { exact: true }).fill(localName)
  const overrideRequest = page.waitForRequest(request => request.method() === 'PATCH' && new URL(request.url()).pathname === `/api/v1/nodes/${nodeID}`)
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: localName, exact: true })).toBeVisible()
  const overrideBody = (await overrideRequest).postDataJSON()
  expect(overrideBody.name).toBe(localName)
  expect(overrideBody.binding_revision).toBeTruthy()
  await expect(page.getByRole('button', { name: '恢复 /name 为来源值', exact: true })).toBeVisible()

  await page.goto(`/sources/${sourceID}/edit`)
  await page.getByLabel('来源地址操作', { exact: true }).selectOption('replace')
  await page.getByLabel('来源地址新值', { exact: true }).fill(`${fixtureURL}/manual/updated`)
  await page.getByRole('button', { name: '保存来源修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: sourceName, exact: true })).toBeVisible()
  await refreshAndOpenPreview(page)
  await expect(page.getByText('上游修改', { exact: true })).toBeVisible()
  await expect(page.getByText('上游变化 · 2 个字段', { exact: true })).toBeVisible()
  await expect(page.getByText('生效变化 · 1 个字段', { exact: true })).toBeVisible()
  await page.getByRole('combobox', { name: '第 1 项操作', exact: true }).selectOption('update')
  await page.getByLabel('我已检查诊断和匹配项，确认将这些选择一次性写入节点库').check()
  const updateRequest = page.waitForRequest(request => request.method() === 'POST' && /\/api\/v1\/imports\/[0-9a-f-]+\/commit$/.test(new URL(request.url()).pathname))
  await page.getByRole('button', { name: '原子提交 1 项', exact: true }).click()
  await expect(page.getByRole('heading', { name: '导入已完成', exact: true })).toBeVisible()
  const updateBody = (await updateRequest).postDataJSON()
  expect(updateBody.source_revision).toBeTruthy()
  expect(updateBody.decisions[0]).toMatchObject({ action: 'update', resource_id: nodeID })
  expect(updateBody.decisions[0].expected_binding_revision).toBeTruthy()

  const updatedRead = await page.request.get(`/api/v1/nodes/${nodeID}`)
  expect(updatedRead.status()).toBe(200)
  const updated = (await updatedRead.json()).data
  expect(updated.metadata.resource_id).toBe(nodeID)
  expect(updated.metadata.name).toBe(localName)
  expect(updated.node.endpoint.port).toBe(18081)
  expect(updated.binding.overridden_fields).toContain('/name')

  await page.goto(`/nodes/${nodeID}`)
  await page.getByRole('button', { name: '恢复 /name 为来源值', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Upstream Beta', exact: true })).toBeVisible()
  const restoredRead = await page.request.get(`/api/v1/nodes/${nodeID}`)
  expect(restoredRead.status()).toBe(200)
  const restored = (await restoredRead.json()).data
  expect(restored.metadata.resource_id).toBe(nodeID)
  expect(restored.binding.overridden_fields).not.toContain('/name')
  await emptyStorage(page)
})

test('safe updates are read-only in their preview and source candidates retain selection across pages', async ({ page }) => {
  await login(page)
  const safeID = await createSource(page, `${prefix}-safe`, '/safe/initial', 'safe_updates')
  const safeBatchID = await refreshAndOpenPreview(page)
  await expect(page.getByText('已自动应用 · 只读', { exact: true })).toBeVisible()
  await expect(page.getByRole('combobox', { name: '第 1 项操作', exact: true })).toBeDisabled()
  await expect(page.getByRole('button', { name: '原子提交 0 项', exact: true })).toBeDisabled()
  const safePreview = await page.request.get(`/api/v1/imports/${safeBatchID}?limit=200`)
  expect(safePreview.status()).toBe(200)
  const safeBody = await safePreview.json()
  const rejectedReplay = await page.request.post(`/api/v1/imports/${safeBatchID}/commit`, {
    headers: { ...(await mutationHeaders(page)), 'If-Match': safePreview.headers().etag, 'Idempotency-Key': randomUUID() },
    data: { source_revision: safeBody.data.source_revision, decisions: [{ candidate_id: safeBody.data.candidates[0].candidate_id, action: 'create' }] },
  })
  expect(rejectedReplay.status()).toBe(409)
  const safeNodes = await listNodes(page, 'Safe Alpha')
  expect(safeNodes).toHaveLength(1)
  const safeRead = await page.request.get(`/api/v1/nodes/${safeNodes[0].metadata.resource_id}`)
  expect(safeRead.status()).toBe(200)
  expect((await safeRead.json()).data.binding.source_resource_id).toBe(safeID)

  await createSource(page, `${prefix}-paged`, '/paged')
  await refreshAndOpenPreview(page)
  await expect(page.getByTestId('import-candidate')).toHaveCount(200)
  await page.getByRole('button', { name: '选择本页新节点', exact: true }).click()
  await expect(page.getByText('已选择 200 / 5000 项（跨页保留）', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '下一页', exact: true }).click()
  await expect(page.getByTestId('import-candidate')).toHaveCount(5)
  await page.getByRole('button', { name: '选择本页新节点', exact: true }).click()
  await expect(page.getByText('已选择 205 / 5000 项（跨页保留）', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '上一页', exact: true }).click()
  await expect(page.getByText('已选择 205 / 5000 项（跨页保留）', { exact: true })).toBeVisible()
  await emptyStorage(page)
})

test('409 and 412 keep browser drafts while source address and bearer token remain masked and omitted', async ({ page }) => {
  await login(page)
  const raceName = `${prefix}-race`
  const raceID = await createSource(page, raceName, '/manual/conflict')
  const batchID = await refreshAndOpenPreview(page)
  await page.getByRole('combobox', { name: '第 1 项操作', exact: true }).selectOption('create')
  const sourceRead = await page.request.get(`/api/v1/sources/${raceID}`)
  expect(sourceRead.status()).toBe(200)
  const concurrent = await page.request.patch(`/api/v1/sources/${raceID}`, {
    headers: { ...(await mutationHeaders(page)), 'If-Match': sourceRead.headers().etag }, data: { name: `${raceName}-advanced` },
  })
  expect(concurrent.status()).toBe(200)
  await page.getByLabel('我已检查诊断和匹配项，确认将这些选择一次性写入节点库').check()
  const conflictResponse = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/api/v1/imports/${batchID}/commit`)
  await page.getByRole('button', { name: '原子提交 1 项', exact: true }).click()
  expect((await conflictResponse).status()).toBe(409)
  await expect(page.getByRole('heading', { name: '预览或节点修订冲突 · 选择已保留', exact: true })).toBeVisible()
  await expect(page.getByText('已选择 1 / 5000 项（跨页保留）', { exact: true })).toBeVisible()
  const superseded = await page.request.get(`/api/v1/imports/${batchID}?limit=200`)
  expect(superseded.status()).toBe(200)
  expect((await superseded.json()).data.state).toBe('superseded')

  const bearer = randomUUID()
  const secretName = `${prefix}-secret-source`
  await page.goto('/sources/new')
  await page.getByLabel('来源名称', { exact: true }).fill(secretName)
  await page.getByLabel('来源地址新值', { exact: true }).fill(`${fixtureURL}/safe/initial`)
  await page.getByRole('combobox', { name: '认证方式', exact: true }).selectOption('bearer')
  await page.getByLabel('Bearer 令牌新值', { exact: true }).fill(bearer)
  await page.getByRole('button', { name: '创建来源', exact: true }).click()
  await expect(page.getByRole('heading', { name: secretName, exact: true })).toBeVisible()
  const secretID = new URL(page.url()).pathname.split('/').at(-1)!
  expect((await page.locator('body').textContent())?.includes(bearer)).toBe(false)
  await emptyStorage(page)

  await page.goto(`/sources/${secretID}/edit`)
  await expect(page.getByLabel('来源地址操作', { exact: true })).toHaveValue('keep')
  await expect(page.getByLabel('Bearer 令牌操作', { exact: true })).toHaveValue('keep')
  const retainedDraft = `${secretName}-my-draft`
  await page.getByLabel('来源名称', { exact: true }).fill(retainedDraft)
  const current = await page.request.get(`/api/v1/sources/${secretID}`)
  expect(current.status()).toBe(200)
  const advanced = await page.request.patch(`/api/v1/sources/${secretID}`, {
    headers: { ...(await mutationHeaders(page)), 'If-Match': current.headers().etag }, data: { name: `${secretName}-server` },
  })
  expect(advanced.status()).toBe(200)
  const staleRequest = page.waitForRequest(request => request.method() === 'PATCH' && new URL(request.url()).pathname === `/api/v1/sources/${secretID}`)
  await page.getByRole('button', { name: '保存来源修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: '来源修订冲突 · 草稿已保留', exact: true })).toBeVisible()
  await expect(page.getByLabel('来源名称', { exact: true })).toHaveValue(retainedDraft)
  const submitted = (await staleRequest).postDataJSON()
  expect(Object.hasOwn(submitted, 'source')).toBe(false)
  expect(JSON.stringify(submitted).includes(bearer), 'stale source PATCH must omit the retained bearer token').toBe(false)
  await emptyStorage(page)
})

test('Base64 node export requires reauthentication and downloads a strictly decodable secret-bearing URI list', async ({ page }) => {
  await login(page)
  const name = `${prefix}-export`
  const credential = randomUUID()
  await page.goto('/nodes/new')
  await page.getByLabel('节点名称', { exact: true }).fill(name)
  await page.getByRole('combobox', { name: '协议', exact: true }).selectOption('shadowsocks')
  await page.getByRole('textbox', { name: /^服务器地址/ }).fill('export.example.invalid')
  await page.getByLabel('端口', { exact: true }).fill('8388')
  await page.getByLabel('节点密码新值', { exact: true }).fill(credential)
  await page.getByRole('button', { name: '创建节点', exact: true }).click()
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible()

  await page.goto('/nodes')
  await page.getByLabel('名称搜索', { exact: true }).fill(name)
  await page.getByRole('button', { name: '筛选', exact: true }).click()
  await page.getByLabel(`选择 ${name}`, { exact: true }).check()
  await page.getByRole('combobox', { name: '导出格式', exact: true }).selectOption('base64_uri_list')
  let exportsSeen = 0
  page.on('request', request => { if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/exports') exportsSeen++ })
  await page.getByRole('button', { name: '导出选中节点', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '再次验证身份', exact: true })).toBeVisible()
  expect(exportsSeen).toBe(0)
  await page.getByLabel('管理员密码', { exact: true }).fill(administratorPassword)
  const exportRequest = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/exports')
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: '验证身份', exact: true }).click()
  const requested = await exportRequest
  expect(requested.postDataJSON().format).toBe('base64_uri_list')
  const file = await download
  expect(file.suggestedFilename()).toBe('nodes-base64.txt')
  const stream = await file.createReadStream()
  const chunks: Buffer[] = []
  for await (const chunk of stream) chunks.push(Buffer.from(chunk))
  const encoded = Buffer.concat(chunks).toString('utf8')
  const decoded = Buffer.from(encoded, 'base64').toString('utf8')
  expect(/^[A-Za-z0-9+/]+={0,2}$/.test(encoded), 'download must use standard Base64 text').toBe(true)
  expect(Buffer.from(decoded, 'utf8').toString('base64') === encoded, 'download must round-trip strict Base64').toBe(true)
  expect(decoded.startsWith('ss://'), 'decoded artifact must be a Shadowsocks URI list').toBe(true)
  const exportedURI = new URL(decoded.trim())
  const userInfo = Buffer.from(exportedURI.username, 'base64url').toString('utf8')
  expect(userInfo.endsWith(`:${credential}`), 'decoded Shadowsocks userinfo must retain the selected synthetic credential').toBe(true)
  expect((await page.locator('body').textContent())?.includes(credential), 'exported secret must not remain in the DOM').toBe(false)
  await expect(page.getByText('已开始下载 1 个导出文件。', { exact: true })).toBeVisible()
  await file.delete()
  await emptyStorage(page)
})
