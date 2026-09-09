import { test, expect, type Page, type Route } from '@playwright/test'
import { randomUUID } from 'node:crypto'
import type { Schema } from '../../src/api/client'
import { newSourceDraft, sourceDraftFromResource, sourceCreateRequest, sourcePatchRequest } from '../../src/domain/source-form'
import { draftFromResource, patchRequest } from '../../src/domain/node-form'

const identity = { user_id: randomUUID(), username: 'source-fixture', role: 'administrator', session_expires_at: '2099-01-01T00:00:00Z', csrf_token: 'x'.repeat(43) }
function source(name = 'Synthetic source'): Schema<'SourceResource'> {
  return { metadata: { resource_id: randomUUID(), scope_id: randomUUID(), kind: 'source', schema_version: 1, security_epoch: '1', name, tags: [], enabled: true, revision: '1' }, source: { schema_version: 1, url_display: 'https://example.com', has_url: true, format: 'uri_list', auth: { kind: 'bearer', has_token: true }, refresh_policy: { enabled: false, interval_seconds: 3600, commit_mode: 'manual', missing_policy: 'retain' }, fetch_limits: { timeout_ms: 15000, max_compressed_bytes: 10485760, max_decoded_bytes: 10485760, max_redirects: 3 }, binding_revision: '1' }, items: [] }
}
function node(name = 'Bound fixture'): Schema<'NodeResource'> {
  return { metadata: { resource_id: randomUUID(), scope_id: randomUUID(), kind: 'node', schema_version: 1, security_epoch: '1', name, tags: [], enabled: true, revision: '1' }, node: { schema_version: 1, protocol: 'trojan', endpoint: { host: 'example.com', port: 443 }, auth: { kind: 'password', has_password: true }, transport: { kind: 'native_tcp' }, security: { mode: 'tls', server_name: 'example.com', verify_certificate: true, alpn: ['h2'] }, features: {}, extensions: {} }, binding: { binding_revision: '7', origin_state: 'active', overridden_fields: ['/name', '/endpoint'], source_resource_id: randomUUID(), source_item_id: randomUUID(), match_method: 'stable_external_key' } }
}
const respond = (route: Route, data: unknown, status = 200, extra = {}) => route.fulfill({ status, json: { request_id: 'source-ui-fixture', data, ...extra }, headers: { 'Cache-Control': 'no-store' } })
const fail = (route: Route, status: number, code: string) => route.fulfill({ status, json: { request_id: 'source-ui-fixture', error: { code, message: 'Synthetic fixed error', details: [] } } })
async function mock(page: Page, handler: (route: Route, path: string) => Promise<boolean | void>) {
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    if (path === '/auth/me') return respond(route, identity)
    if (await handler(route, path)) return
    if (path === '/nodes' || path === '/sources') return respond(route, [], 200, { page: { limit: 50 } })
    return fail(route, 404, 'RESOURCE_NOT_FOUND')
  })
}

test('source form defaults are manual, retain and unscheduled; retained URL and authentication never become patches', () => {
  const draft = newSourceDraft()
  expect(draft.refresh).toEqual({ enabled: false, interval_seconds: 3600, commit_mode: 'manual', missing_policy: 'retain' })
  draft.name = 'New source'; draft.url.value = `https://example.com/${randomUUID()}`
  expect(sourceCreateRequest(draft).source.auth).toEqual({ kind: 'none' })
  const saved = source()
  const editing = sourceDraftFromResource(saved)
  expect([editing.url.value, editing.token.value]).toEqual(['', ''])
  editing.name = 'Renamed source'
  expect(sourcePatchRequest(editing, saved)).toEqual({ name: 'Renamed source' })
  editing.token.mode = 'replace'; editing.token.value = randomUUID()
  expect(sourcePatchRequest(editing, saved)).toEqual({ name: 'Renamed source', source: { auth: { kind: 'bearer', token: editing.token.value } } })
  editing.token.value = '********'
  expect(() => sourcePatchRequest(editing, saved)).toThrow('不能提交显示遮罩')
  editing.token.value = randomUUID(); editing.url.mode = 'replace'; editing.url.value = saved.source.url_display
  expect(sourcePatchRequest(editing, saved).source?.url).toBe(saved.source.url_display)
})

test('bound node edits send only modified overlay paths and required discriminators', () => {
  const saved = node()
  const draft = draftFromResource(saved)
  draft.name = 'Local name'
  expect(patchRequest(draft, saved)).toEqual({ name: 'Local name', binding_revision: '7' })
  draft.password.mode = 'replace'; draft.password.value = randomUUID()
  draft.serverName = 'changed.example.com'
  expect(JSON.parse(JSON.stringify(patchRequest(draft, saved)))).toEqual({ name: 'Local name', binding_revision: '7', node: { auth: { kind: 'password', password: draft.password.value }, security: { mode: 'tls', server_name: 'changed.example.com' } } })
  draft.port = 8443
  expect(patchRequest(draft, saved).node?.endpoint).toEqual({ host: 'example.com', port: 8443 })
  draft.protocol = 'vless'
  expect(() => patchRequest(draft, saved)).toThrow()
})

test('create source submits defaults and credentials without putting them into page text or storage', async ({ page }) => {
  const saved = source('Created source')
  const secretURL = `https://example.com/${randomUUID()}?token=${randomUUID()}`
  const credential = randomUUID()
  let submitted: Schema<'SourceCreateRequest'> | undefined
  await mock(page, async (route, path) => {
    if (path === '/sources' && route.request().method() === 'POST') { submitted = route.request().postDataJSON(); await respond(route, saved, 201); return true }
    if (path === `/sources/${saved.metadata.resource_id}`) { await respond(route, saved); return true }
  })
  await page.goto('/sources/new')
  await expect(page.getByLabel('启用周期刷新', { exact: true })).not.toBeChecked()
  await page.getByLabel('来源名称', { exact: true }).fill(saved.metadata.name)
  await page.getByLabel('来源地址新值', { exact: true }).fill(secretURL)
  await page.getByRole('combobox', { name: '认证方式', exact: true }).selectOption('bearer')
  await page.getByLabel('Bearer 令牌新值', { exact: true }).fill(credential)
  await page.getByRole('button', { name: '创建来源', exact: true }).click()
  await expect(page.getByRole('heading', { name: saved.metadata.name, exact: true })).toBeVisible()
  expect(submitted?.source.url).toBe(secretURL)
  expect(submitted?.source.auth).toEqual({ kind: 'bearer', token: credential })
  expect(submitted?.source.refresh_policy).toEqual({ enabled: false, interval_seconds: 3600, commit_mode: 'manual', missing_policy: 'retain' })
  expect(await page.locator('body').innerText()).not.toContain(credential)
  expect(await page.locator('body').innerText()).not.toContain(secretURL)
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0])
})

test('source conflict preserves URL and auth drafts; unchanged credentials stay omitted on retry', async ({ page }) => {
  const saved = source()
  const requests: { body: Schema<'SourcePatchRequest'>; etag: string }[] = []
  await mock(page, async (route, path) => {
    if (path !== `/sources/${saved.metadata.resource_id}`) return
    if (route.request().method() === 'PATCH') {
      requests.push({ body: route.request().postDataJSON(), etag: route.request().headers()['if-match']! })
      if (requests.length === 1) { saved.metadata.revision = '2'; saved.metadata.name = 'Concurrent source'; await fail(route, 409, 'STATE_CONFLICT') }
      else { saved.metadata.name = requests.at(-1)!.body.name!; saved.metadata.revision = '3'; await respond(route, saved) }
    } else await respond(route, saved)
    return true
  })
  await page.goto(`/sources/${saved.metadata.resource_id}/edit`)
  await expect(page.getByLabel('来源地址操作', { exact: true })).toHaveValue('keep')
  await expect(page.getByLabel('来源地址新值', { exact: true })).toHaveCount(0)
  await page.getByLabel('来源名称', { exact: true }).fill('Retained draft')
  await page.getByRole('button', { name: '保存来源修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: '来源修订冲突 · 草稿已保留' })).toBeVisible()
  await expect(page.getByLabel('来源名称', { exact: true })).toHaveValue('Retained draft')
  await page.getByRole('button', { name: '读取最新来源进行比较', exact: true }).click()
  await page.getByLabel('已比较来源差异', { exact: true }).check()
  await page.getByRole('button', { name: '保留草稿，采用最新修订号', exact: true }).click()
  await page.getByRole('button', { name: '保存来源修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Retained draft', exact: true })).toBeVisible()
  expect(requests).toEqual([{ body: { name: 'Retained draft' }, etag: '"r1"' }, { body: { name: 'Retained draft' }, etag: '"r2"' }])
})

test('manual refresh polls the job and exposes the latest preview link with recent success', async ({ page }) => {
  const saved = source()
  const jobID = randomUUID()
  const batchID = randomUUID()
  let reads = 0
  let headers: Record<string, string> = {}
  const task: Schema<'Job'> = { job_id: jobID, revision: '1', executor: 'api_worker', type: 'source_refresh', state: 'queued', attempt: 0, lease_seq: '0', cancel_requested: false, created_at: '2026-01-01T00:00:00Z' }
  await mock(page, async (route, path) => {
    if (path === `/sources/${saved.metadata.resource_id}`) { await respond(route, saved); return true }
    if (path === `/sources/${saved.metadata.resource_id}/refresh`) { headers = route.request().headers(); saved.source.last_job_id = jobID; await respond(route, task, 202); return true }
    if (path === `/jobs/${jobID}`) {
      reads++
      if (reads > 1) { task.state = 'succeeded'; saved.source.latest_preview_batch_id = batchID; saved.source.last_success_at = '2026-01-02T00:00:00Z' }
      await respond(route, task); return true
    }
  })
  await page.goto(`/sources/${saved.metadata.resource_id}`)
  await page.getByRole('button', { name: '手动刷新', exact: true }).click()
  await expect(page.getByRole('link', { name: '查看最新来源预览', exact: true })).toHaveAttribute('href', `/imports/${batchID}`)
  await expect(page.getByText('刷新完成', { exact: true })).toBeVisible()
  expect(headers['if-match']).toBe('"r1"')
  expect(headers['idempotency-key']).toBeTruthy()
  expect(reads).toBeGreaterThan(1)
})

test('source preview protects applied and unchanged rows across pages and sends frozen source and binding preconditions', async ({ page }) => {
  const batchID = randomUUID()
  const sourceID = randomUUID()
  const candidate = (index: number, change: Schema<'ImportCandidate'>['change_kind'] = 'new'): Schema<'ImportCandidate'> => ({ candidate_id: randomUUID(), index, state: change === 'new' ? 'new' : 'matched', change_kind: change, source_item_id: randomUUID(), name: `Source candidate ${index + 1}`, node: node().node, diagnostics: [], upstream_changes: [], effective_changes: [] })
  const applied = { ...candidate(1), auto_applied: true }
  const unchanged = candidate(2, 'unchanged')
  const missing = candidate(3, 'missing')
  const changed = { ...candidate(4, 'modified'), existing_resource_id: randomUUID(), existing_revision: '4', binding_revision: '8', upstream_changes: [{ field_path: '/auth/password', secret_changed: true as const }, { field_path: '/name', before: 'Old label', after: 'New label' }], effective_changes: [{ field_path: '/auth/password', secret_changed: true as const }] }
  const suggested = { ...candidate(5, 'conflict'), existing_resource_id: randomUUID(), existing_revision: '6', binding_revision: '9', match_method: 'suggestion' as const }
  const skip = candidate(6)
  const first = candidate(0)
  const secondPage = candidate(7)
  const candidates = [first, applied, unchanged, missing, changed, suggested, skip, secondPage]
  let submitted: Schema<'ImportCommitRequest'> | undefined
  let committed = false
  await mock(page, async (route, path) => {
    if (path === `/imports/${batchID}`) {
      const more = new URL(route.request().url()).searchParams.has('cursor')
      await respond(route, { batch_id: batchID, job_id: randomUUID(), revision: '2', source_id: sourceID, source_revision: '3', snapshot_id: randomUUID(), state: committed ? 'committed' : 'ready', candidate_count: candidates.length, candidates: more ? [secondPage] : candidates.slice(0, 7), created_at: '2026-01-01T00:00:00Z', expires_at: '2099-01-01T00:00:00Z', diagnostics: [] }, 200, { page: { limit: 200, ...(!more ? { next_cursor: 'second' } : {}) } }); return true
    }
    if (path === `/imports/${batchID}/commit`) {
      submitted = route.request().postDataJSON(); committed = true
      expect(route.request().headers()['if-match']).toBe('"r2"')
      await respond(route, { batch_id: batchID, revision: '3', items: submitted!.decisions.map(item => ({ candidate_id: item.candidate_id, status: item.action === 'create' ? 'created' : item.action === 'update' ? 'updated' : item.action === 'bind' ? 'bound' : 'skipped' })) }); return true
    }
  })
  await page.goto(`/imports/${batchID}`)
  await expect(page.getByRole('heading', { name: '来源刷新预览', exact: true })).toBeVisible()
  for (const index of [2, 3, 4]) await expect(page.getByLabel(`第 ${index} 项操作`, { exact: true })).toBeDisabled()
  const changedRow = page.getByTestId('import-candidate').filter({ hasText: 'Source candidate 5' })
  await changedRow.getByText('上游变化 · 2 个字段', { exact: true }).click()
  await expect(changedRow.getByText('秘密已变化', { exact: true }).first()).toBeVisible()
  await expect(changedRow.getByText('"Old label"', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '选择全部新节点', exact: true }).click()
  await expect(page.getByText('已选择 3 / 5000 项（跨页保留）', { exact: true })).toBeVisible()
  await page.getByLabel('第 5 项操作', { exact: true }).selectOption('update')
  await page.getByLabel('第 6 项操作', { exact: true }).selectOption('bind')
  await page.getByLabel('第 7 项操作', { exact: true }).selectOption('skip')
  await page.getByRole('button', { name: '下一页', exact: true }).click()
  await expect(page.getByLabel('第 8 项操作', { exact: true })).toHaveValue('create')
  await page.getByLabel('我已检查诊断和匹配项，确认将这些选择一次性写入节点库').check()
  await page.getByRole('button', { name: '原子提交 5 项', exact: true }).click()
  await expect(page.getByRole('heading', { name: '导入已完成', exact: true })).toBeVisible()
  expect(submitted?.source_revision).toBe('3')
  expect(submitted?.binding_revision).toBeUndefined()
  expect(submitted?.decisions.find(item => item.candidate_id === changed.candidate_id)).toEqual({ candidate_id: changed.candidate_id, action: 'update', resource_id: changed.existing_resource_id, expected_revision: '4', expected_binding_revision: '8' })
  expect(submitted?.decisions.find(item => item.candidate_id === suggested.candidate_id)).toEqual({ candidate_id: suggested.candidate_id, action: 'bind', resource_id: suggested.existing_resource_id, expected_revision: '6', expected_binding_revision: '9' })
  expect(submitted?.decisions.some(item => [applied, unchanged, missing].some(readonly => item.candidate_id === readonly.candidate_id))).toBe(false)
})

test('superseded source previews are read-only and point back to the source', async ({ page }) => {
  const batchID = randomUUID()
  const sourceID = randomUUID()
  await mock(page, async (route, path) => {
    if (path !== `/imports/${batchID}`) return
    await respond(route, { batch_id: batchID, job_id: randomUUID(), revision: '3', source_id: sourceID, source_revision: '2', state: 'superseded', candidate_count: 1, candidates: [{ candidate_id: randomUUID(), index: 0, state: 'new', change_kind: 'new', name: 'Old preview', node: node().node, diagnostics: [] }], created_at: '2026-01-01T00:00:00Z', expires_at: '2099-01-01T00:00:00Z', diagnostics: [] }, 200, { page: { limit: 200 } }); return true
  })
  await page.goto(`/imports/${batchID}`)
  await expect(page.getByRole('heading', { name: '预览已被替代', exact: true })).toBeVisible()
  await expect(page.getByLabel('第 1 项操作', { exact: true })).toBeDisabled()
  await expect(page.getByRole('heading', { name: '确认写入', exact: true })).toHaveCount(0)
  await expect(page.getByRole('link', { name: '查看来源最新状态', exact: true })).toHaveAttribute('href', `/sources/${sourceID}`)
})

test('bound node edit keeps its draft after a binding conflict and retries using the latest binding revision', async ({ page }) => {
  const saved = node()
  const requests: Schema<'NodePatchRequest'>[] = []
  await mock(page, async (route, path) => {
    if (path !== `/nodes/${saved.metadata.resource_id}`) return
    if (route.request().method() === 'PATCH') {
      requests.push(route.request().postDataJSON())
      if (requests.length === 1) { saved.binding!.binding_revision = '8'; saved.metadata.revision = '2'; await fail(route, 409, 'STATE_CONFLICT') }
      else { saved.metadata.name = requests.at(-1)!.name!; saved.metadata.revision = '3'; await respond(route, saved) }
    } else await respond(route, saved)
    return true
  })
  await page.goto(`/nodes/${saved.metadata.resource_id}/edit`)
  await expect(page.getByRole('combobox', { name: /^协议/ })).toBeDisabled()
  await page.getByLabel('节点名称', { exact: true }).fill('Local overlay name')
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: '修订冲突 · 草稿已保留', exact: true })).toBeVisible()
  await expect(page.getByLabel('节点名称', { exact: true })).toHaveValue('Local overlay name')
  await page.getByRole('button', { name: '加载最新版本进行比较', exact: true }).click()
  await page.getByLabel('已比较差异，确认基于最新修订继续编辑').check()
  await page.getByRole('button', { name: '保留草稿，采用最新修订号', exact: true }).click()
  await page.getByRole('button', { name: '保存新修订', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Local overlay name', exact: true })).toBeVisible()
  expect(requests).toEqual([{ name: 'Local overlay name', binding_revision: '7' }, { name: 'Local overlay name', binding_revision: '8' }])
})

test('field restore retains the operation on 412 and sends only the chosen restore path', async ({ page }) => {
  const saved = node()
  const requests: { body: Schema<'NodePatchRequest'>; etag: string }[] = []
  await mock(page, async (route, path) => {
    if (path !== `/nodes/${saved.metadata.resource_id}`) return
    if (route.request().method() === 'PATCH') {
      requests.push({ body: route.request().postDataJSON(), etag: route.request().headers()['if-match']! })
      if (requests.length === 1) { saved.metadata.revision = '2'; saved.binding!.binding_revision = '8'; await fail(route, 412, 'REVISION_MISMATCH') }
      else { saved.metadata.revision = '3'; saved.binding!.binding_revision = '9'; saved.binding!.overridden_fields = ['/endpoint']; await respond(route, saved) }
    } else await respond(route, saved)
    return true
  })
  await page.goto(`/nodes/${saved.metadata.resource_id}`)
  await page.getByRole('button', { name: '恢复 /name 为来源值', exact: true }).click()
  await expect(page.getByRole('heading', { name: '绑定修订冲突 · 操作已保留', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '读取最新绑定进行比较', exact: true }).click()
  await page.getByLabel('已比较绑定差异，仍要执行原操作').check()
  await page.getByRole('button', { name: '采用最新修订重试操作', exact: true }).click()
  await expect(page.getByRole('button', { name: '恢复 /name 为来源值', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '恢复 /endpoint 为来源值', exact: true })).toBeVisible()
  expect(requests).toEqual([{ body: { restore_fields: ['/name'], binding_revision: '7' }, etag: '"r1"' }, { body: { restore_fields: ['/name'], binding_revision: '8' }, etag: '"r2"' }])
})

test('export requires reauthentication, uses selected revisions and releases each secret Blob URL for all three formats', async ({ page }) => {
  const saved = node('Export fixture')
  const credential = randomUUID()
  const exports: Schema<'ResourceExportRequest'>[] = []
  let authenticated = false
  await page.addInitScript(() => {
    const originalCreate = URL.createObjectURL.bind(URL)
    const originalRevoke = URL.revokeObjectURL.bind(URL)
    const evidence = { created: 0, revoked: 0 }
    Object.defineProperty(window, '__blobEvidence', { value: evidence })
    URL.createObjectURL = blob => { evidence.created++; return originalCreate(blob) }
    URL.revokeObjectURL = url => { evidence.revoked++; originalRevoke(url) }
  })
  await mock(page, async (route, path) => {
    if (path === '/nodes') { await respond(route, [saved], 200, { page: { limit: 50 } }); return true }
    if (path === '/auth/reauth') { authenticated = true; await respond(route, { ...identity, recent_authentication_expires_at: '2099-01-01T00:00:00Z' }); return true }
    if (path === '/exports') {
      expect(authenticated).toBe(true)
      exports.push(route.request().postDataJSON())
      await respond(route, { created_at: '2026-01-01T00:00:00Z', artifacts: [{ filename: exports.at(-1)!.format === 'proxyloom_json' ? 'proxyloom.json' : 'proxyloom.txt', media_type: 'text/plain', content: credential, contains_secrets: true }] }); return true
    }
  })
  await page.goto('/nodes')
  await page.getByLabel('选择 Export fixture', { exact: true }).check()
  await page.getByRole('button', { name: '导出选中节点', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '再次验证身份', exact: true })).toBeVisible()
  expect(exports).toHaveLength(0)
  await page.getByRole('button', { name: '取消', exact: true }).click()
  expect(exports).toHaveLength(0)
  for (const format of ['uri_list', 'base64_uri_list', 'proxyloom_json'] as const) {
    await page.getByRole('combobox', { name: '导出格式', exact: true }).selectOption(format)
    const download = page.waitForEvent('download')
    await page.getByRole('button', { name: '导出选中节点', exact: true }).click()
    if (!authenticated) { await page.getByLabel('管理员密码', { exact: true }).fill(randomUUID()); await page.getByRole('button', { name: '验证身份', exact: true }).click() }
    await download
    await expect(page.getByRole('button', { name: '导出选中节点', exact: true })).toBeEnabled()
  }
  expect(exports.map(item => item.format)).toEqual(['uri_list', 'base64_uri_list', 'proxyloom_json'])
  for (const item of exports) expect(item).toEqual({ type: 'resources', format: item.format, include_secrets: true, resources: [{ resource_id: saved.metadata.resource_id, revision: '1', kind: 'node' }] })
  await expect.poll(() => page.evaluate(() => (window as unknown as { __blobEvidence: { created: number; revoked: number } }).__blobEvidence)).toEqual({ created: 3, revoked: 3 })
  expect(await page.locator('body').innerText()).not.toContain(credential)
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0])
})

test('node export never exceeds 200 selected resources across pages', async ({ page }) => {
  const nodes = Array.from({ length: 201 }, (_, index) => node(`Limit fixture ${index + 1}`))
  let count = 0
  await mock(page, async (route, path) => {
    if (path === '/nodes') {
      const second = new URL(route.request().url()).searchParams.get('cursor') === 'second'
      await respond(route, second ? nodes.slice(200) : nodes.slice(0, 200), 200, { page: { limit: 200, ...(!second ? { next_cursor: 'second' } : {}) } }); return true
    }
    if (path === '/auth/reauth') { await respond(route, { ...identity, recent_authentication_expires_at: '2099-01-01T00:00:00Z' }); return true }
    if (path === '/exports') { count = route.request().postDataJSON().resources.length; await respond(route, { artifacts: [{ filename: 'bounded.txt', media_type: 'text/plain', content: '', contains_secrets: true }], created_at: '2026-01-01T00:00:00Z' }); return true }
  })
  await page.goto('/nodes')
  await page.getByLabel('选择本页全部节点', { exact: true }).check()
  await page.getByRole('button', { name: '下一页', exact: true }).click()
  await page.getByLabel('选择 Limit fixture 201', { exact: true }).check()
  await expect(page.getByRole('alert')).toContainText('最多选择 200 个节点')
  await expect(page.getByText('已选择 200 / 200 个节点（跨页保留）', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '导出选中节点', exact: true }).click()
  await page.getByLabel('管理员密码', { exact: true }).fill(randomUUID())
  await page.getByRole('button', { name: '验证身份', exact: true }).click()
  await expect.poll(() => count).toBe(200)
})
