import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { chromium } from '../proxyloom-web/node_modules/@playwright/test/index.mjs'

const baseURL = process.env.PROXYLOOM_E2E_BASE_URL
const username = process.env.PROXYLOOM_E2E_USERNAME
const password = process.env.PROXYLOOM_E2E_PASSWORD
const container = process.env.PROXYLOOM_ACCEPTANCE_DB_CONTAINER
assert.match(baseURL ?? '', /^http:\/\/127\.0\.0\.1:\d+$/, 'loopback base URL required')
assert.ok(username && password, 'synthetic administrator credentials required')
assert.match(container ?? '', /^proxyloom-acceptance-[0-9a-f-]{36}$/, 'owned acceptance container required')

function docker(args) {
  const result = spawnSync('docker', args, { encoding: 'utf8', windowsHide: true, timeout: 20_000 })
  assert.ok(!result.error && result.status === 0, `docker_${args[0]}_failed`)
  return result.stdout.trim()
}

const browser = await chromium.launch({ channel: 'chrome', headless: true })
const context = await browser.newContext({ baseURL })
const page = await context.newPage()
let disconnected = false
let publishedPort = ''
const result = { expired_preview: 'NOT_RUN', management_api_unavailable: 'NOT_RUN', ui_error: 'NOT_RUN', database_restored: 'NOT_RUN' }
try {
  await page.goto('/login')
  await page.locator('[name="username"]').fill(username)
  await page.locator('[name="password"]').fill(password)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('heading', { name: '节点库', exact: true }).waitFor()

  const marker = `acceptance-expired-${randomUUID().slice(0, 8)}`
  await page.goto('/imports')
  await page.getByRole('combobox', { name: '输入格式', exact: true }).selectOption('uri_list')
  await page.getByRole('textbox', { name: '分享链接内容', exact: true }).fill(`http://example.com:18080#${marker}`)
  await page.getByRole('button', { name: '解析并预览', exact: true }).click()
  await page.getByRole('heading', { name: '确认写入', exact: true }).waitFor({ timeout: 45_000 })
  const batchID = new URL(page.url()).pathname.split('/').at(-1)
  assert.match(batchID ?? '', /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
  const changed = docker(['exec', container, 'psql', '-U', 'proxyloom_bootstrap', '-d', 'proxyloom', '-v', 'ON_ERROR_STOP=1', '-tAc',
    `UPDATE public.import_batches SET created_at=CURRENT_TIMESTAMP-interval '8 days',expires_at=CURRENT_TIMESTAMP-interval '1 second' WHERE id='${batchID}'::uuid RETURNING id`])
  const updateLines = changed.split(/\r?\n/)
  assert.ok(updateLines.includes(batchID) && updateLines.includes('UPDATE 1'), 'only the selected import batch must be changed')
  await page.reload()
  await page.getByRole('heading', { name: '预览已过期', exact: true }).waitFor({ timeout: 15_000 })
  assert.equal(await page.getByTestId('import-candidate').count(), 0)
  result.expired_preview = 'PASS'

  await page.goto('/nodes')
  await page.getByRole('heading', { name: '节点库', exact: true }).waitFor()
  publishedPort = docker(['inspect', '--format', '{{(index (index .NetworkSettings.Ports "5432/tcp") 0).HostPort}}', container])
  assert.match(publishedPort, /^\d+$/)
  docker(['network', 'disconnect', container, container])
  disconnected = true
  const unavailableResponse = page.waitForResponse(response => response.url().includes('/api/v1/nodes?') && response.status() === 503)
  await page.getByRole('button', { name: '筛选', exact: true }).click()
  const unavailable = await unavailableResponse
  const unavailableBody = await unavailable.json()
  assert.equal(unavailableBody.error?.code, 'SERVICE_UNAVAILABLE')
  assert.equal(Object.hasOwn(unavailableBody, 'data'), false)
  result.management_api_unavailable = 'PASS'
  await page.getByText('服务暂时不可用，请稍后重试。', { exact: true }).waitFor()
  result.ui_error = 'PASS'
} finally {
  if (disconnected) {
    docker(['network', 'connect', container, container])
    disconnected = false
  }
  for (let attempt = 0; attempt < 20; attempt++) {
    try {
      const response = await page.request.get('/readyz')
      if (response.status() === 200) {
        result.database_restored = 'PASS'
        break
      }
    } catch {}
    await new Promise(resolve => setTimeout(resolve, 250))
  }
  if (result.database_restored === 'PASS' && publishedPort) {
    assert.equal(docker(['inspect', '--format', '{{(index (index .NetworkSettings.Ports "5432/tcp") 0).HostPort}}', container]), publishedPort)
    const recoveredResponse = page.waitForResponse(response => response.url().includes('/api/v1/nodes?') && response.status() === 200)
    await page.reload()
    await recoveredResponse
    await page.getByRole('heading', { name: '节点库', exact: true }).waitFor()
  }
  await context.close()
  await browser.close()
}
assert.equal(result.database_restored, 'PASS')
console.log(JSON.stringify(result))
