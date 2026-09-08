import { test, expect, type Page, type BrowserContext } from '@playwright/test'
import { randomUUID } from 'node:crypto'

const username = process.env.PROXYLOOM_E2E_USERNAME!
const administratorPassword = process.env.PROXYLOOM_E2E_PASSWORD!
const baseURL = process.env.PROXYLOOM_E2E_BASE_URL!
const prefix = `ui-${randomUUID().slice(0, 8)}`
const cleanup = new Set<string>()
let importMutationPossible = false
// Reuse only cookies in worker memory. Keep the app's CSRF/session restoration real and never write storageState files.
let cachedCookies: Awaited<ReturnType<BrowserContext['cookies']>> = []

async function login(page: Page) {
  if (cachedCookies.length) {
    await page.context().addCookies(cachedCookies)
    await page.goto('/nodes')
    await expect(page.getByRole('heading', { name: '节点库', exact: true })).toBeVisible()
    return
  }
  await page.goto('/login')
  await page.locator('[name="username"]').fill(username)
  await page.locator('[name="password"]').fill(administratorPassword)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '节点库', exact: true })).toBeVisible()
  cachedCookies = await page.context().cookies()
}
async function headers(page: Page) {
  const me = await page.request.get('/api/v1/auth/me')
  expect(me.status()).toBe(200)
  const session = await me.json()
  return { Origin: new URL(baseURL).origin, 'X-CSRF-Token': session.data.csrf_token, 'Content-Type': 'application/json' }
}
async function emptyStorage(page: Page) {
  expect(await page.evaluate(() => ({ local: localStorage.length, session: sessionStorage.length }))).toEqual({ local: 0, session: 0 })
}
async function createNode(page: Page, protocol: string, suffix: string) {
  const name = `${prefix}-${suffix}`
  const credential = randomUUID()
  await page.goto('/nodes/new')
  await page.getByLabel('节点名称', { exact: true }).fill(name)
  await page.getByRole('combobox', { name: '协议', exact: true }).selectOption(protocol)
  await page.getByLabel('服务器地址').fill('example.com')
  await page.getByLabel('端口', { exact: true }).fill('443')
  if (protocol === 'shadowsocks' || protocol === 'trojan') await page.getByLabel('节点密码新值', { exact: true }).fill(credential)
  if (protocol === 'vless' || protocol === 'vmess') await page.getByLabel('UUID新值', { exact: true }).fill(credential)
  if (protocol === 'trojan') {
    await page.getByLabel('服务器名称（SNI）', { exact: true }).fill('example.com')
    await page.getByLabel('ALPN（可选）', { exact: true }).fill('h2, http/1.1')
    await page.getByLabel('客户端指纹（可选）', { exact: true }).fill('chrome')
  }
  await page.getByRole('button', { name: '创建节点', exact: true }).click()
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible()
  const id = new URL(page.url()).pathname.split('/').at(-1)!
  cleanup.add(id)
  await emptyStorage(page)
  return { id, name, credential }
}
async function collectImported(page: Page) {
  let cursor = ''
  do {
    const response = await page.request.get(`/api/v1/nodes?limit=200&q=${encodeURIComponent(prefix)}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`)
    expect(response.status()).toBe(200)
    const body = await response.json()
    for (const item of body.data) cleanup.add(item.metadata.resource_id)
    cursor = body.page.next_cursor ?? ''
  } while (cursor)
}

test.afterEach(async ({ page }) => {
  if (cachedCookies.length) cachedCookies = await page.context().cookies()
  if (importMutationPossible) { await collectImported(page); importMutationPossible = false }
  if (!cleanup.size) return
  const auth = await headers(page)
  const failures: number[] = []
  for (const id of cleanup) {
    const current = await page.request.get(`/api/v1/nodes/${id}`)
    if (current.status() === 404) continue
    if (current.status() !== 200) { failures.push(current.status()); continue }
    const deleted = await page.request.delete(`/api/v1/nodes/${id}`, { headers: { ...auth, 'If-Match': current.headers().etag } })
    if (deleted.status() !== 200) failures.push(deleted.status())
  }
  cleanup.clear()
  expect(failures, 'All synthetic test nodes must be cleaned').toEqual([])
})

test.afterAll(async ({ browser }) => {
  if (!cachedCookies.length) return
  const context = await browser.newContext({ baseURL })
  try {
    await context.addCookies(cachedCookies)
    const session = await context.request.get('/api/v1/auth/me')
    if (session.status() === 200) {
      const body = await session.json()
      const response = await context.request.post('/api/v1/auth/logout', { headers: { Origin: new URL(baseURL).origin, 'X-CSRF-Token': body.data.csrf_token }, data: {} })
      expect(response.status(), 'Revoke the synthetic shared test session').toBe(200)
    } else expect(session.status()).toBe(401)
  } finally { cachedCookies = []; await context.close() }
})

test('initialization when requested, login, reload session restore and logout use real authentication', async ({ page }) => {
  if (process.env.PROXYLOOM_E2E_SETUP_TOKEN) {
    await page.goto('/setup')
    await page.locator('[name="username"]').fill(username)
    await page.locator('[name="password"]').fill(administratorPassword)
    await page.locator('[name="setup-token"]').fill(process.env.PROXYLOOM_E2E_SETUP_TOKEN)
    await page.getByRole('button', { name: '创建管理员并进入', exact: true }).click()
    await expect(page.getByRole('heading', { name: '节点库', exact: true })).toBeVisible()
    await page.getByRole('button', { name: '退出', exact: true }).click()
    await expect(page.getByRole('heading', { name: '欢迎回来', exact: true })).toBeVisible()
  }
  await login(page)
  await page.reload()
  await expect(page.getByRole('heading', { name: '节点库', exact: true })).toBeVisible()
  await emptyStorage(page)
  await page.getByRole('button', { name: '退出', exact: true }).click()
  await expect(page.getByRole('heading', { name: '欢迎回来', exact: true })).toBeVisible()
  expect((await page.request.get('/api/v1/auth/me')).status()).toBe(401)
  cachedCookies = []
})

test('six protocol forms, secret-preserving edits, server clone, revisions, references, batch metadata and UI deletion', async ({ page }) => {
  await login(page)
  const created = []
  for (const protocol of ['shadowsocks', 'vmess', 'vless', 'trojan', 'socks5', 'http']) created.push(await createNode(page, protocol, protocol))
  const first = created[0]
  await page.goto(`/nodes/${first.id}/edit`)
  await expect(page.getByLabel('节点密码操作')).toHaveValue('keep')
  const editedName = `${first.name}-edited`
  await page.getByLabel('节点名称', { exact: true }).fill(editedName)
  const patchPromise = page.waitForRequest(request => request.method() === 'PATCH' && request.url().endsWith(`/nodes/${first.id}`))
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  const patch = await patchPromise
  expect(patch.postDataJSON().node.auth.password).toBeUndefined()
  await expect(page.getByRole('heading', { name: editedName, exact: true })).toBeVisible()
  await page.getByRole('button', { name: '克隆', exact: true }).click()
  await page.getByLabel('副本名称', { exact: true }).fill(`${prefix}-clone`)
  const cloneRequest = page.waitForRequest(request => request.method() === 'POST' && request.url().endsWith(`/nodes/${first.id}/clone`))
  await page.getByRole('button', { name: '创建副本', exact: true }).click()
  const clone = await cloneRequest
  expect(Object.keys(clone.postDataJSON())).toEqual(['name'])
  await expect(page.getByRole('heading', { name: `${prefix}-clone`, exact: true })).toBeVisible()
  const cloneID = new URL(page.url()).pathname.split('/').at(-1)!
  cleanup.add(cloneID)
  await page.goto(`/nodes/${first.id}`)
  await page.getByRole('tab', { name: '修订历史', exact: true }).click()
  await expect(page.getByRole('heading', { name: '不可变修订历史' })).toBeVisible()
  await expect(page.locator('.revision-item')).toHaveCount(2)
  await page.getByRole('tab', { name: '引用关系', exact: true }).click()
  await expect(page.getByText('此筛选下没有引用。', { exact: true })).toBeVisible()
  const tlsNode = created.find(item => item.name.endsWith('-trojan'))!
  await page.goto(`/nodes/${tlsNode.id}/edit`)
  await page.getByLabel('ALPN（可选）', { exact: true }).fill('')
  await page.getByLabel('客户端指纹（可选）', { exact: true }).fill('')
  const clearRequest = page.waitForRequest(request => request.method() === 'PATCH' && request.url().endsWith(`/nodes/${tlsNode.id}`))
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  const cleared = await clearRequest
  expect(cleared.postDataJSON().node.security.alpn).toEqual([])
  expect(cleared.postDataJSON().node.security.client_fingerprint).toBeNull()
  await expect(page.getByRole('heading', { name: tlsNode.name, exact: true })).toBeVisible()
  const readCleared = await page.request.get(`/api/v1/nodes/${tlsNode.id}`)
  expect(readCleared.status()).toBe(200)
  const afterClear = await readCleared.json()
  expect(afterClear.data.node.security.alpn).toBeUndefined()
  expect(afterClear.data.node.security.client_fingerprint).toBeUndefined()
  await page.goto('/nodes')
  await page.getByLabel('名称搜索', { exact: true }).fill(prefix)
  await page.getByRole('button', { name: '筛选', exact: true }).click()
  await expect(page.getByTestId('node-row')).toHaveCount(7)
  await page.getByLabel('选择本页全部节点').check()
  await page.getByRole('combobox', { name: '批量操作', exact: true }).selectOption('add_tags')
  await page.getByLabel('操作标签', { exact: true }).fill('验收标签')
  await page.getByRole('button', { name: '应用到选中节点', exact: true }).click()
  await expect(page.getByText('成功 7，未成功 0。未成功的选择已保留。', { exact: true })).toBeVisible()
  await page.getByLabel('选择本页全部节点').check()
  await page.getByRole('combobox', { name: '批量操作', exact: true }).selectOption('disable')
  await page.getByRole('button', { name: '应用到选中节点', exact: true }).click()
  await expect(page.getByTestId('node-row').filter({ hasText: '已停用' })).toHaveCount(7)
  await page.getByLabel('选择本页全部节点').check()
  await page.getByRole('combobox', { name: '批量操作', exact: true }).selectOption('enable')
  await page.getByRole('button', { name: '应用到选中节点', exact: true }).click()
  await expect(page.getByTestId('node-row').filter({ hasText: '已启用' })).toHaveCount(7)
  await page.goto(`/nodes/${cloneID}`)
  await page.getByRole('button', { name: '删除节点', exact: true }).click()
  const deleteDialog = page.getByRole('dialog', { name: '删除节点', exact: true })
  await expect(deleteDialog).toBeVisible()
  const confirmDelete = deleteDialog.getByRole('button', { name: '确认删除', exact: true })
  await expect(confirmDelete).toBeDisabled()
  const confirmation = deleteDialog.getByLabel('输入完整节点名称确认', { exact: true })
  await confirmation.fill(prefix)
  await expect(confirmDelete).toBeDisabled()
  await confirmation.fill(`${prefix}-clone`)
  await expect(confirmDelete).toBeEnabled()
  const deletion = page.waitForResponse(response => response.request().method() === 'DELETE' && new URL(response.url()).pathname === `/api/v1/nodes/${cloneID}`)
  await confirmDelete.click()
  expect((await deletion).status()).toBe(200)
  await expect(page.getByRole('heading', { name: '节点库', exact: true })).toBeVisible()
  await expect(page).toHaveURL(new URL('/nodes', baseURL).href)
  expect((await page.request.get(`/api/v1/nodes/${cloneID}`)).status()).toBe(404)
  cleanup.delete(cloneID)
  await emptyStorage(page)
})

test('real concurrent revision conflict preserves draft until latest-version comparison is acknowledged', async ({ page }) => {
  await login(page)
  const created = await createNode(page, 'http', 'conflict')
  await page.goto(`/nodes/${created.id}/edit`)
  const localName = `${prefix}-my-draft`
  await page.getByLabel('节点名称', { exact: true }).fill(localName)
  const current = await page.request.get(`/api/v1/nodes/${created.id}`)
  const changed = await page.request.patch(`/api/v1/nodes/${created.id}`, { headers: { ...(await headers(page)), 'If-Match': current.headers().etag }, data: { name: `${prefix}-concurrent-change` } })
  expect(changed.status()).toBe(200)
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: '修订冲突 · 草稿已保留' })).toBeVisible()
  await expect(page.getByLabel('节点名称', { exact: true })).toHaveValue(localName)
  await page.getByRole('button', { name: '加载最新版本进行比较', exact: true }).click()
  await expect(page.getByRole('heading', { name: '服务器最新 · r2' })).toBeVisible()
  await page.getByLabel('已比较差异，确认基于最新修订继续编辑').check()
  await page.getByRole('button', { name: '保留草稿，采用最新修订号', exact: true }).click()
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: localName, exact: true })).toBeVisible()
})

test('secret reveal requires reauthentication and clears the displayed material on close', async ({ page }) => {
  await login(page)
  const created = await createNode(page, 'shadowsocks', 'reveal')
  await page.getByRole('button', { name: '验证身份并查看秘密', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '再次验证身份' })).toBeVisible()
  await page.getByLabel('管理员密码', { exact: true }).fill(administratorPassword)
  await page.getByRole('button', { name: '验证身份', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '节点秘密 · 临时查看' })).toBeVisible()
  expect(await page.locator('.secret-display').textContent()).toContain(created.credential)
  await emptyStorage(page)
  await page.getByRole('button', { name: '隐藏并清除', exact: true }).click()
  await expect(page.locator('.secret-display')).toHaveCount(0)
})

test('text import stays preview-only, diagnoses bad and duplicate lines, preserves selection across 200-item pages and commits atomically', async ({ page }) => {
  test.setTimeout(180_000)
  await login(page)
  const unique = Array.from({ length: 205 }, (_, index) => `http${'://'}example.com:${20000 + index}#${prefix}-import-${index}`)
  const input = [...unique, unique[0], 'malformed input', `unknown${'://'}example.com:443`].join('\n')
  await page.goto('/imports')
  await page.getByRole('combobox', { name: '输入格式', exact: true }).selectOption('uri_list')
  await page.getByLabel('分享链接内容', { exact: true }).fill(input)
  await page.getByRole('button', { name: '解析并预览', exact: true }).click()
  await expect(page.getByRole('heading', { name: '确认写入', exact: true })).toBeVisible({ timeout: 45_000 })
  const before = await page.request.get(`/api/v1/nodes?q=${prefix}-import`)
  expect((await before.json()).data).toHaveLength(0)
  await expect(page.getByTestId('import-candidate')).toHaveCount(200)
  await page.getByRole('button', { name: '选择本页新节点', exact: true }).click()
  await page.getByRole('button', { name: '下一页', exact: true }).click()
  await expect(page.getByTestId('import-candidate')).toHaveCount(8)
  await expect(page.getByText('重复或冲突', { exact: true })).toBeVisible()
  await expect(page.getByText('无效条目', { exact: true })).toHaveCount(2)
  await page.getByRole('button', { name: '选择本页新节点', exact: true }).click()
  await expect(page.getByText('已选择 205 / 5000 项（跨页保留）', { exact: true })).toBeVisible()
  await page.getByLabel('我已检查诊断和匹配项，确认将这些选择一次性写入节点库').check()
  const sent = page.waitForRequest(request => request.method() === 'POST' && /\/imports\/[^/]+\/commit$/.test(new URL(request.url()).pathname))
  importMutationPossible = true
  await page.getByRole('button', { name: '原子提交 205 项', exact: true }).click()
  const request = await sent
  expect(request.postDataJSON().decisions).toHaveLength(205)
  expect(request.headers()['if-match']).toBeTruthy()
  expect(request.headers()['idempotency-key']).toBeTruthy()
  await expect(page.getByRole('heading', { name: '导入已完成', exact: true })).toBeVisible({ timeout: 45_000 })
  await collectImported(page)
  expect(cleanup.size).toBe(205)
  await emptyStorage(page)
})

test('file import honors explicit Base64 format and can resume its redacted preview after reload', async ({ page }) => {
  await login(page)
  await page.goto('/imports')
  await page.getByRole('combobox', { name: '输入方式', exact: true }).selectOption('file')
  await page.getByRole('combobox', { name: '输入格式', exact: true }).selectOption('base64_uri_list')
  const contents = Buffer.from(`socks5${'://'}example.com:1080#${prefix}-file`).toString('base64')
  await page.locator('input[type=file]').setInputFiles({ name: 'synthetic.txt', mimeType: 'text/plain', buffer: Buffer.from(contents) })
  await page.getByRole('button', { name: '解析并预览', exact: true }).click()
  await expect(page.getByRole('heading', { name: '确认写入', exact: true })).toBeVisible({ timeout: 45_000 })
  await page.reload()
  await expect(page.getByTestId('import-candidate')).toHaveCount(1)
  await page.getByRole('button', { name: '选择本页新节点', exact: true }).click()
  await page.getByLabel('我已检查诊断和匹配项，确认将这些选择一次性写入节点库').check()
  importMutationPossible = true
  await page.getByRole('button', { name: '原子提交 1 项', exact: true }).click()
  await expect(page.getByRole('heading', { name: '导入已完成', exact: true })).toBeVisible()
  await collectImported(page)
  expect(cleanup.size).toBe(1)
})
