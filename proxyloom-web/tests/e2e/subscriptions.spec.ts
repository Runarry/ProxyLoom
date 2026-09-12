import { test, expect, type Page } from '@playwright/test'
import { spawnSync } from 'node:child_process'

const seedID = process.env.PROXYLOOM_M2_PROFILE!
const password = process.env.PROXYLOOM_E2E_PASSWORD!
let createdID = ''
async function login(page: Page) {
  await page.goto('/login')
  await page.locator('[name="username"]').fill(process.env.PROXYLOOM_E2E_USERNAME!)
  await page.locator('[name="password"]').fill(password)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '节点库', exact: true })).toBeVisible()
}
async function headers(page: Page, revision?: string) {
  const me = await page.request.get('/api/v1/auth/me'); expect(me.status()).toBe(200)
  const value = (await me.json()).data
  return { Origin: new URL(process.env.PROXYLOOM_E2E_BASE_URL!).origin, 'X-CSRF-Token': value.csrf_token, 'Content-Type': 'application/json', ...(revision ? { 'If-Match': `"r${revision}"` } : {}) }
}
async function read(page: Page, path: string) { const r = await page.request.get(`/api/v1${path}`); expect(r.status()).toBe(200); return (await r.json()).data }
async function reauth(page: Page) {
  const dialog = page.getByRole('dialog')
  if (await dialog.isVisible()) { await dialog.locator('input[type=password]').fill(password); await dialog.getByRole('button', { name: '验证身份', exact: true }).click() }
}
test.describe.configure({ mode: 'serial' })
test.beforeEach(async ({ page }) => { await login(page) })
test.afterEach(async ({ page }) => { expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0]) })

test('create three targets, validate real kernels, confirm publication and revoke a mobile link', async ({ page }) => {
  const seed = await read(page, `/subscriptions/${seedID}`)
  const cores = await read(page, '/cores'), presets = await read(page, '/client-presets')
  await page.goto('/subscriptions/new')
  await page.getByLabel('名称', { exact: true }).fill('M2 browser publication')
  await page.getByRole('combobox', { name: '手动包含', exact: true }).selectOption(seed.subscription.members.include_ids[0])
  await page.getByRole('button', { name: '添加包含成员', exact: true }).click()
  await page.getByRole('combobox', { name: '路由方案', exact: true }).selectOption(seed.subscription.routing_profile_id)
  await page.getByRole('combobox', { name: 'DNS 方案', exact: true }).selectOption(seed.subscription.dns_profile_id)
  for (const [index, family] of ['xray', 'sing-box', 'mihomo'].entries()) {
    await page.getByRole('button', { name: '添加输出目标', exact: true }).click()
    const core = cores.find((c: any) => c.core_family === family && c.architecture === 'amd64')
    const preset = presets.find((p: any) => p.preset.core_family === family && !p.preset.control_api.enabled)
    await page.getByRole('combobox', { name: '内核构建', exact: true }).nth(index).selectOption(core.core_build_id)
    await page.getByRole('combobox', { name: '客户端预设', exact: true }).nth(index).selectOption(preset.metadata.resource_id)
    await page.getByLabel('目标键', { exact: true }).nth(index).fill(`${family}-default`)
  }
  await page.getByRole('button', { name: '保存订阅方案', exact: true }).click()
  await expect(page).toHaveURL(/\/subscriptions\/[0-9a-f-]{36}$/)
  createdID = new URL(page.url()).pathname.split('/').at(-1)!
  await page.getByRole('button', { name: '编译并检查全部目标', exact: true }).click()
  await expect(page.getByText('校验通过，等待确认发布', { exact: true })).toBeVisible({ timeout: 60000 })
  const publish = page.getByRole('button', { name: '确认发布全部目标', exact: true })
  await expect(publish).toBeDisabled()
  await page.getByLabel('已检查全部目标和差异，确认实际依赖中列出的凭证将随订阅分发', { exact: true }).check()
  await publish.click()
  await expect(page.getByText('所有启用目标已作为同一版本发布。', { exact: true })).toBeVisible()
  const resource = await read(page, `/subscriptions/${createdID}`)
  expect(resource.publication_head.generation).toBe('1')
  const publications = await read(page, `/subscriptions/${createdID}/publications`)
  expect(publications[0].targets).toHaveLength(3)
  await page.getByRole('button', { name: '编译并检查全部目标', exact: true }).click()
  await expect(page.getByText('校验通过，等待确认发布', { exact: true })).toBeVisible({ timeout: 60000 })
  await page.getByLabel('已检查全部目标和差异，确认实际依赖中列出的凭证将随订阅分发', { exact: true }).check()
  await publish.click()
  await expect.poll(async () => (await read(page, `/subscriptions/${createdID}`)).publication_head.generation).toBe('2')
  await page.getByRole('button', { name: '回滚到此版本', exact: true }).last().click()
  await expect(page.getByText('已创建新的回滚发布记录。', { exact: true })).toBeVisible()
  expect((await read(page, `/subscriptions/${createdID}`)).publication_head.generation).toBe('3')
  await page.getByLabel('令牌名称', { exact: true }).fill('mobile')
  await page.getByRole('button', { name: '签发独立令牌', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await reauth(page)
  await expect(page.getByRole('heading', { name: '请保存此次签发的链接', exact: true })).toBeVisible()
  await page.setViewportSize({ width: 390, height: 844 })
  const link = await page.getByLabel('xray-default 订阅链接', { exact: true }).inputValue()
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  const firstLink = await page.getByRole('textbox', { name: /订阅链接$/ }).first().inputValue()
  await page.getByRole('button', { name: '复制链接', exact: true }).first().click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(firstLink)
  const downloaded = await page.request.get(link)
  expect(downloaded.status()).toBe(200)
  expect(downloaded.headers()['cache-control']).toBe('private, no-store')
  expect((await downloaded.body()).length).toBeGreaterThan(100)
  const database = process.env.PROXYLOOM_M2_DB_CONTAINER!
  expect(database).toMatch(/^proxyloom-acceptance-[0-9a-f-]{36}$/)
  const docker = (args: string[]) => { const r = spawnSync('docker', args, { encoding: 'utf8', windowsHide: true, timeout: 20000 }); expect(r.status).toBe(0); return r.stdout.trim() }
  expect(docker(['inspect', '--format', '{{index .Config.Labels "io.proxyloom.acceptance"}}', database])).toBe(database.slice('proxyloom-acceptance-'.length))
  docker(['network', 'disconnect', database, database])
  try { const unavailable = await page.request.get(link); expect(unavailable.status()).toBe(503); expect((await unavailable.json()).error.code).toBe('SERVICE_UNAVAILABLE') }
  finally { docker(['network', 'connect', database, database]) }
  await expect.poll(async () => (await page.request.get(link)).status(), { timeout: 20000 }).toBe(200)
  await page.getByRole('button', { name: '已保存，关闭展示', exact: true }).click()
  await page.reload()
  await expect(page.getByRole('heading', { name: '请保存此次签发的链接', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: '轮换', exact: true }).click()
  await expect(page.getByRole('heading', { name: '请保存此次签发的链接', exact: true })).toBeVisible()
  const rotatedLink = await page.getByLabel('xray-default 订阅链接', { exact: true }).inputValue()
  expect((await page.request.get(link)).status()).toBe(404)
  expect((await page.request.get(rotatedLink)).status()).toBe(200)
  await page.getByRole('button', { name: '已保存，关闭展示', exact: true }).click()
  await page.getByRole('button', { name: '撤销', exact: true }).first().click()
  await expect(page.getByText('已撤销', { exact: true })).toHaveCount(2)
  expect((await page.request.get(rotatedLink)).status()).toBe(404)
})

test('concurrent editing preserves the draft and cloning retains resource references', async ({ page }) => {
  const original = await read(page, `/subscriptions/${seedID}`)
  await page.goto(`/subscriptions/${seedID}/edit`)
  await page.getByLabel('名称', { exact: true }).fill('M2 retained draft')
  const changed = await page.request.patch(`/api/v1/subscriptions/${seedID}`, { headers: await headers(page, original.metadata.revision), data: { name: 'M2 concurrent edit' } })
  expect(changed.status()).toBe(200)
  await page.getByRole('button', { name: '保存订阅方案', exact: true }).click()
  await expect(page.getByRole('heading', { name: '修订冲突 · 草稿已保留', exact: true })).toBeVisible()
  await expect(page.getByLabel('名称', { exact: true })).toHaveValue('M2 retained draft')
  await page.getByRole('button', { name: '加载最新版本进行比较', exact: true }).click()
  await page.getByLabel('已比较差异，确认基于最新修订继续编辑', { exact: true }).check()
  await page.getByRole('button', { name: '保留草稿，采用最新修订号', exact: true }).click()
  await page.getByRole('button', { name: '保存订阅方案', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'M2 retained draft', exact: true })).toBeVisible()
  await page.getByText('复制与删除', { exact: true }).click()
  await page.getByLabel('副本名称', { exact: true }).fill('M2 cloned profile')
  await page.getByRole('button', { name: '复制方案', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'M2 cloned profile', exact: true })).toBeVisible()
  const cloneID = new URL(page.url()).pathname.split('/').at(-1)!
  expect(cloneID).not.toBe(seedID)
  const clone = await read(page, `/subscriptions/${cloneID}`)
  expect(clone.subscription.members).toEqual(original.subscription.members)
  expect(clone.publication_head.generation).toBe('0')
})

test('obsolete input requires recompile and disabled build blocks history', async ({ page }) => {
  await page.goto(`/subscriptions/${createdID}`)
  const response = page.waitForResponse(r => r.url().endsWith(`/subscriptions/${createdID}/compile`) && r.request().method() === 'POST')
  await page.getByRole('button', { name: '编译并检查全部目标', exact: true }).click()
  const compile = await response; expect(compile.status()).toBe(202)
  const batch = (await compile.json()).data
  const resource = await read(page, `/subscriptions/${createdID}`)
  expect((await page.request.patch(`/api/v1/subscriptions/${createdID}`, { headers: await headers(page, resource.metadata.revision), data: { name: 'M2 obsolete input' } })).status()).toBe(200)
  await page.getByRole('button', { name: '刷新检查结果', exact: true }).click()
  await expect(page.getByText('输入已变化，需要重新编译', { exact: true })).toBeVisible()
  expect((await read(page, `/compile-batches/${batch.batch_id}`)).state).toBe('obsolete')
  await page.reload()
  const cores = await read(page, '/cores'), core = cores.find((c: any) => c.core_family === 'xray' && c.architecture === 'amd64')
  expect((await page.request.post(`/api/v1/cores/${core.core_build_id}/disable`, { headers: await headers(page, core.revision), data: { reason: 'M2 acceptance' } })).status()).toBe(200)
  await page.reload()
  await expect(page.getByText('当前依赖或授权不再允许使用此版本。', { exact: true }).first()).toBeVisible()
  const publications = await read(page, `/subscriptions/${createdID}/publications`)
  expect(publications[0].state).toBe('blocked')
})
