import { test, expect, type Page, type Route } from '@playwright/test'
import { randomUUID } from 'node:crypto'

const identity = { user_id: randomUUID(), username: 'fixture', role: 'administrator', session_expires_at: '2099-01-01T00:00:00Z', csrf_token: 'x'.repeat(43) }
function resource(name = 'Fixture node', revision = '1') {
  return { metadata: { resource_id: randomUUID(), scope_id: randomUUID(), kind: 'node', schema_version: 1, security_epoch: '1', name, tags: [], enabled: true, revision }, node: { schema_version: 1, protocol: 'http', endpoint: { host: 'example.com', port: 443 }, auth: { kind: 'none' }, transport: { kind: 'native_tcp' }, security: { mode: 'none' }, features: {}, extensions: {} } }
}
const respond = (route: Route, data: unknown, status = 200, extra = {}) => route.fulfill({ status, json: { request_id: 'ui-fixture', data, ...extra }, headers: { 'Cache-Control': 'no-store' } })
const fail = (route: Route, status: number, code: string) => route.fulfill({ status, json: { request_id: 'ui-fixture', error: { code, message: 'Synthetic fixed error', details: [] } } })
async function mock(page: Page, handler: (route: Route, path: string) => Promise<boolean | void>) {
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    if (path === '/auth/me') return respond(route, identity)
    if (await handler(route, path)) return
    if (path === '/nodes') return respond(route, [], 200, { page: { limit: 50 } })
    return fail(route, 404, 'RESOURCE_NOT_FOUND')
  })
}

test('restoring an existing session can recover from an initial service outage and return to the requested page', async ({ page }) => {
  let reads = 0
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith('/auth/me')) {
      reads += 1
      if (reads === 1) return fail(route, 503, 'SERVICE_UNAVAILABLE')
      return respond(route, identity)
    }
    return respond(route, [], 200, { page: { limit: 50 } })
  })
  await page.goto('/nodes')
  await expect(page.getByRole('heading', { name: '欢迎回来', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '重试恢复会话', exact: true }).click()
  await expect(page.getByRole('heading', { name: '节点库', exact: true })).toBeVisible()
})

test('switching away from a secret protocol never submits its hidden fields and writes no browser storage', async ({ page }) => {
  const created = resource('HTTP fixture')
  let submitted: Record<string, unknown> | undefined
  await mock(page, async (route, path) => {
    if (path === '/nodes' && route.request().method() === 'POST') { submitted = route.request().postDataJSON(); await respond(route, created, 201); return true }
    if (path === `/nodes/${created.metadata.resource_id}`) { await respond(route, created); return true }
  })
  await page.goto('/nodes/new')
  await page.getByLabel('节点名称', { exact: true }).fill('HTTP fixture')
  await page.getByLabel('服务器地址').fill('example.com')
  const synthetic = randomUUID()
  await page.getByLabel('节点密码新值', { exact: true }).fill(synthetic)
  await page.getByRole('combobox', { name: '协议', exact: true }).selectOption('http')
  await expect(page.getByLabel('节点密码新值', { exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: '创建节点', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'HTTP fixture', exact: true })).toBeVisible()
  expect(JSON.stringify(submitted)).not.toContain(synthetic)
  expect(submitted?.node).toMatchObject({ protocol: 'http', auth: { kind: 'none' }, transport: { kind: 'native_tcp' }, security: { mode: 'none' } })
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0])
})

test('412 keeps the entire draft, compares a newer redacted resource and only retries after explicit acknowledgement', async ({ page }) => {
  const saved = resource('Server original')
  let patches = 0
  const requests: { etag: string; body: { name: string } }[] = []
  await mock(page, async (route, path) => {
    if (path !== `/nodes/${saved.metadata.resource_id}`) return
    if (route.request().method() === 'PATCH') {
      patches += 1
      const body = route.request().postDataJSON()
      requests.push({ etag: route.request().headers()['if-match'], body })
      if (patches === 1) await fail(route, 412, 'REVISION_MISMATCH')
      else { saved.metadata.name = body.name; saved.metadata.revision = '3'; await respond(route, saved) }
    } else {
      if (patches === 1) { saved.metadata.name = 'Concurrent server name'; saved.metadata.revision = '2' }
      await respond(route, saved)
    }
    return true
  })
  await page.goto(`/nodes/${saved.metadata.resource_id}/edit`)
  await page.getByLabel('节点名称', { exact: true }).fill('My retained draft')
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: '修订冲突 · 草稿已保留' })).toBeVisible()
  await expect(page.getByLabel('节点名称', { exact: true })).toHaveValue('My retained draft')
  await expect(page.getByRole('button', { name: '保存新修订', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: '加载最新版本进行比较', exact: true }).click()
  await expect(page.getByRole('heading', { name: '服务器最新 · r2' })).toBeVisible()
  await expect(page.getByRole('button', { name: '保留草稿，采用最新修订号' })).toBeDisabled()
  await page.getByLabel('已比较差异，确认基于最新修订继续编辑').check()
  await page.getByRole('button', { name: '保留草稿，采用最新修订号' }).click()
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'My retained draft', exact: true })).toBeVisible()
  expect(requests.map(item => item.etag)).toEqual(['"r1"', '"r2"'])
  expect(requests[1].body.name).toBe('My retained draft')
})

test('clearing optional TLS fields sends explicit clear values while leaving credentials omitted', async ({ page }) => {
  const base = resource('TLS clear fixture')
  const saved = { ...base, node: { ...base.node, protocol: 'trojan', auth: { kind: 'password', has_password: true }, security: { mode: 'tls', server_name: 'example.com', verify_certificate: true, alpn: ['h2'], client_fingerprint: 'chrome' } } }
  let cleared = false
  let submitted: { node: { security: Record<string, unknown>; auth: Record<string, unknown> } } | undefined
  const read = () => cleared ? { ...saved, metadata: { ...saved.metadata, revision: '2' }, node: { ...saved.node, security: { mode: 'tls', server_name: 'example.com', verify_certificate: true } } } : saved
  await mock(page, async (route, path) => {
    if (path !== `/nodes/${saved.metadata.resource_id}`) return
    if (route.request().method() === 'PATCH') { submitted = route.request().postDataJSON(); cleared = true }
    await respond(route, read()); return true
  })
  await page.goto(`/nodes/${saved.metadata.resource_id}/edit`)
  await page.getByLabel('ALPN（可选）', { exact: true }).fill('')
  await page.getByLabel('客户端指纹（可选）', { exact: true }).fill('')
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'TLS clear fixture', exact: true })).toBeVisible()
  expect(submitted?.node.security.alpn).toEqual([])
  expect(submitted?.node.security.client_fingerprint).toBeNull()
  expect(submitted?.node.auth.password).toBeUndefined()
})

test('cross-page import selection commits 205 items once and retries an uncertain response with the same idempotency key', async ({ page }) => {
  const batchID = randomUUID()
  const candidates = Array.from({ length: 205 }, (_, index) => ({ candidate_id: randomUUID(), index, state: 'new', name: `Candidate ${index}`, node: resource().node, diagnostics: [] }))
  let committed = false
  let commits = 0
  const requests: { key: string; count: number; etag: string }[] = []
  await mock(page, async (route, path) => {
    if (path === `/imports/${batchID}`) {
      const cursor = new URL(route.request().url()).searchParams.get('cursor')
      await respond(route, { batch_id: batchID, job_id: randomUUID(), revision: '2', state: committed ? 'committed' : 'ready', candidate_count: 205, candidates: cursor ? candidates.slice(200) : candidates.slice(0, 200), created_at: '2026-01-01T00:00:00Z', expires_at: '2099-01-01T00:00:00Z', diagnostics: [] }, 200, { page: { limit: 200, ...(cursor ? {} : { next_cursor: 'page-two' }) } }); return true
    }
    if (path === `/imports/${batchID}/commit`) {
      commits += 1
      const body = route.request().postDataJSON()
      requests.push({ key: route.request().headers()['idempotency-key'], count: body.decisions.length, etag: route.request().headers()['if-match'] })
      if (commits === 1) await route.abort('failed')
      else { committed = true; await respond(route, { batch_id: batchID, revision: '3', items: candidates.map(candidate => ({ candidate_id: candidate.candidate_id, status: 'created', resource_id: randomUUID(), revision: '1' })) }) }
      return true
    }
  })
  await page.goto(`/imports/${batchID}`)
  await expect(page.getByTestId('import-candidate')).toHaveCount(200)
  await page.getByRole('button', { name: '选择本页新节点', exact: true }).click()
  await page.getByRole('button', { name: '下一页', exact: true }).click()
  await expect(page.getByTestId('import-candidate')).toHaveCount(5)
  await page.getByRole('button', { name: '选择本页新节点', exact: true }).click()
  await page.getByLabel('我已检查诊断和匹配项，确认将这些选择一次性写入节点库').check()
  await page.getByRole('button', { name: '原子提交 205 项', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('无法连接服务')
  await page.getByRole('button', { name: '原子提交 205 项', exact: true }).click()
  await expect(page.getByRole('heading', { name: '导入已完成', exact: true })).toBeVisible()
  expect(requests).toHaveLength(2)
  expect(requests[0].count).toBe(205)
  expect(requests[0].etag).toBe('"r2"')
  expect(requests[0].key).toBeTruthy()
  expect(requests[1]).toEqual(requests[0])
})

test('expired import cannot be selected or committed', async ({ page }) => {
  const id = randomUUID()
  await mock(page, async (route, path) => {
    if (path !== `/imports/${id}`) return
    await respond(route, { batch_id: id, job_id: randomUUID(), revision: '2', state: 'expired', candidate_count: 0, candidates: [], created_at: '2020-01-01T00:00:00Z', expires_at: '2020-01-02T00:00:00Z', diagnostics: [] }, 200, { page: { limit: 200 } }); return true
  })
  await page.goto(`/imports/${id}`)
  await expect(page.getByRole('heading', { name: '预览已过期', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: /原子提交/ })).toHaveCount(0)
  await expect(page.getByRole('link', { name: '重新导入', exact: true })).toBeVisible()
})

test('import update conflict loads the real current target revision and retains the choice until comparison acknowledgement', async ({ page }) => {
  const batchID = randomUUID()
  const target = resource('Changed server target', '2')
  const candidate = { candidate_id: randomUUID(), index: 0, state: 'matched', name: 'Import candidate', node: resource().node, diagnostics: [], existing_resource_id: target.metadata.resource_id, existing_revision: '1', match_method: 'exact_fingerprint' }
  const revisions: string[] = []
  await mock(page, async (route, path) => {
    if (path === `/nodes/${target.metadata.resource_id}`) { await respond(route, target); return true }
    if (path === `/imports/${batchID}`) {
      await respond(route, { batch_id: batchID, job_id: randomUUID(), revision: '2', state: 'ready', candidate_count: 1, candidates: [candidate], created_at: '2026-01-01T00:00:00Z', expires_at: '2099-01-01T00:00:00Z', diagnostics: [] }, 200, { page: { limit: 200 } }); return true
    }
    if (path === `/imports/${batchID}/commit`) {
      revisions.push(route.request().postDataJSON().decisions[0].expected_revision)
      if (revisions.length === 1) await fail(route, 412, 'REVISION_MISMATCH')
      else await respond(route, { batch_id: batchID, revision: '3', items: [{ candidate_id: candidate.candidate_id, status: 'updated', resource_id: target.metadata.resource_id, revision: '3' }] })
      return true
    }
  })
  await page.goto(`/imports/${batchID}`)
  await page.getByRole('combobox', { name: '第 1 项操作', exact: true }).selectOption('update')
  await page.getByLabel('我已检查诊断和匹配项，确认将这些选择一次性写入节点库').check()
  await page.getByRole('button', { name: '原子提交 1 项', exact: true }).click()
  await expect(page.getByRole('heading', { name: '预览或节点修订冲突 · 选择已保留' })).toBeVisible()
  await page.getByRole('button', { name: '重新读取预览进行比较', exact: true }).click()
  await expect(page.getByText('Changed server target · 选择时 r1 → 最新 r2', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '已比较，采用最新修订', exact: true }).click()
  await page.getByLabel('我已检查诊断和匹配项，确认将这些选择一次性写入节点库').check()
  await page.getByRole('button', { name: '原子提交 1 项', exact: true }).click()
  await expect(page.getByRole('heading', { name: '导入已完成', exact: true })).toBeVisible()
  expect(revisions).toEqual(['1', '2'])
})

test('select all is bounded at 5000 across 25 pages and refuses a 5001st choice without losing an existing selection', async ({ page }) => {
  test.setTimeout(60_000)
  const batchID = randomUUID()
  const boundedID = randomUUID()
  const candidates = Array.from({ length: 5001 }, (_, index) => ({ candidate_id: randomUUID(), index, state: 'new', name: `Bounded candidate ${index}`, node: resource().node, diagnostics: [] }))
  await mock(page, async (route, path) => {
    if (path !== `/imports/${batchID}` && path !== `/imports/${boundedID}`) return
    const active = path.endsWith(boundedID) ? candidates.slice(0, 5000) : candidates
    const offset = Number(new URL(route.request().url()).searchParams.get('cursor') || 0)
    await respond(route, { batch_id: path.endsWith(boundedID) ? boundedID : batchID, job_id: randomUUID(), revision: '2', state: 'ready', candidate_count: active.length, candidates: active.slice(offset, offset + 200), created_at: '2026-01-01T00:00:00Z', expires_at: '2099-01-01T00:00:00Z', diagnostics: [] }, 200, { page: { limit: 200, ...(offset + 200 < active.length ? { next_cursor: String(offset + 200) } : {}) } }); return true
  })
  await page.goto(`/imports/${batchID}`)
  await page.getByRole('combobox', { name: '第 1 项操作', exact: true }).selectOption('create')
  await page.getByRole('button', { name: '选择全部新节点', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('全部新节点超过 5000 个')
  await expect(page.getByText('已选择 1 / 5000 项（跨页保留）', { exact: true })).toBeVisible()
  await page.goto(`/imports/${boundedID}`)
  await page.getByRole('button', { name: '选择全部新节点', exact: true }).click()
  await expect(page.getByText('已选择 5000 / 5000 项（跨页保留）', { exact: true })).toBeVisible()
})

test('reauthentication failure leaves the authenticated page available, secret reveal is memory-only and closes on Escape', async ({ page }) => {
  const saved = resource('Reveal fixture')
  let authenticated = false
  let reauthAttempts = 0
  const synthetic = randomUUID()
  await mock(page, async (route, path) => {
    if (path === `/nodes/${saved.metadata.resource_id}`) { await respond(route, saved); return true }
    if (path === '/auth/reauth') {
      reauthAttempts += 1
      if (reauthAttempts === 1) await fail(route, 401, 'AUTH_REQUIRED')
      else { authenticated = true; await respond(route, { ...identity, recent_authentication_expires_at: '2099-01-01T00:00:00Z' }) }
      return true
    }
    if (path.endsWith('/reveal')) {
      expect(authenticated).toBe(true)
      await respond(route, { resource_id: saved.metadata.resource_id, revision: '1', auth: { kind: 'password', password: synthetic }, security: { mode: 'tls', server_name: 'example.com', verify_certificate: true } }); return true
    }
  })
  await page.goto(`/nodes/${saved.metadata.resource_id}`)
  await page.getByRole('button', { name: '验证身份并查看秘密', exact: true }).click()
  await page.getByLabel('管理员密码', { exact: true }).fill(randomUUID())
  await page.getByRole('button', { name: '验证身份', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '再次验证身份' })).toBeVisible()
  await expect(page.getByLabel('管理员密码', { exact: true })).toHaveValue('')
  await page.getByLabel('管理员密码', { exact: true }).fill(randomUUID())
  await page.getByRole('button', { name: '验证身份', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '节点秘密 · 临时查看' })).toBeVisible()
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0])
  await page.keyboard.press('Escape')
  await expect(page.locator('.secret-display')).toHaveCount(0)
  await expect(page.getByRole('button', { name: '验证身份并查看秘密', exact: true })).toBeFocused()
})

test('long names and Chinese errors fit mobile width and the main navigation remains keyboard accessible', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const saved = resource('较长的中文节点名称'.repeat(20))
  await mock(page, async (route, path) => {
    if (path === '/nodes') { await respond(route, [saved], 200, { page: { limit: 50 } }); return true }
  })
  await page.goto('/nodes')
  await expect(page.getByTestId('node-row')).toHaveCount(1)
  const overflow = await page.evaluate(() => [...document.querySelectorAll<HTMLElement>('body *')].filter(element => element.getBoundingClientRect().right > window.innerWidth && !element.closest('.table-wrap')).map(element => ({ tag: element.tagName, className: element.className, right: element.getBoundingClientRect().right })))
  expect(overflow).toEqual([])
  const dimensions = await page.evaluate(() => ({ scroll: document.documentElement.scrollWidth, viewport: window.innerWidth }))
  expect(dimensions.scroll).toBeLessThanOrEqual(dimensions.viewport)
  await page.keyboard.press('Tab')
  await expect(page.getByRole('link', { name: '跳转到主要内容' })).toBeFocused()
})
