import { test } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { newDraft, changeProtocol, createRequest, patchRequest, draftFromResource, resetSecurity, resetAuthentication, clearDraftSecrets } from '../src/domain/node-form.ts'

function resource() {
  return {
    metadata: { resource_id: randomUUID(), scope_id: randomUUID(), kind: 'node', name: 'Synthetic fixture', tags: [], enabled: true, revision: '9223372036854775806', schema_version: 1, security_epoch: '1' },
    node: { schema_version: 1, protocol: 'shadowsocks', endpoint: { host: 'example.com', port: 443 }, auth: { kind: 'method_password', method: 'aes-128-gcm', has_password: true }, transport: { kind: 'native_tcp' }, security: { mode: 'none' }, features: {}, extensions: {} },
  }
}

test('redacted edit omits a retained secret, sends explicit null on clear and sends only replacement on replace', () => {
  const baseline = resource()
  const draft = draftFromResource(baseline)
  const kept = JSON.parse(JSON.stringify(patchRequest(draft, baseline)))
  assert.equal('password' in kept.node.auth, false)
  assert.equal(JSON.stringify(kept).includes('has_password'), false)
  draft.password.mode = 'clear'
  assert.equal(patchRequest(draft, baseline).node.auth.password, null)
  draft.password.mode = 'replace'; draft.password.value = randomUUID()
  assert.equal(patchRequest(draft, baseline).node.auth.password, draft.password.value)
  assert.equal(baseline.metadata.revision, '9223372036854775806')
})

test('all protocol switches discard prior authentication, security, transport and feature drafts', () => {
  const all = ['shadowsocks', 'vmess', 'vless', 'trojan', 'socks5', 'http']
  for (const previous of all) for (const next of all.filter(item => item !== previous)) {
    const draft = newDraft(previous)
    Object.assign(draft, { name: 'Preserved metadata', host: 'example.com', port: 1234, transport: 'websocket', wsPath: '/obsolete', wsHost: 'old.example.com', security: 'reality', serverName: 'old.example.com', vision: true })
    for (const key of ['password', 'uuid', 'username', 'publicKey', 'shortID']) draft[key].value = randomUUID()
    const changed = changeProtocol(draft, next)
    assert.equal(changed.name, 'Preserved metadata')
    assert.equal(changed.host, 'example.com')
    assert.equal(changed.transport, 'native_tcp')
    assert.equal(changed.wsHost, '')
    assert.equal(changed.serverName, '')
    assert.equal(changed.vision, false)
    for (const key of ['password', 'uuid', 'username', 'publicKey', 'shortID']) { assert.equal(changed[key].value, ''); assert.equal(changed[key].canKeep, false) }
  }
})

test('HTTP no-auth serialization never includes hidden secret, websocket or REALITY values', () => {
  const draft = newDraft('http')
  Object.assign(draft, { name: 'HTTP fixture', host: 'example.com', transport: 'websocket', security: 'reality', vision: false })
  const marker = randomUUID()
  draft.password.value = marker; draft.publicKey.value = marker; draft.wsHost = marker
  const request = createRequest(draft)
  assert.deepEqual(request.node.auth, { kind: 'none' })
  assert.deepEqual(request.node.transport, { kind: 'native_tcp' })
  assert.deepEqual(request.node.security, { mode: 'none' })
  assert.equal(JSON.stringify(request).includes(marker), false)
})

test('sentinel masks cannot become create or patch credentials; empty REALITY Short ID is distinct from null', () => {
  const baseline = resource()
  const draft = draftFromResource(baseline)
  draft.password.mode = 'replace'
  for (const marker of ['', '********', 'REDACTED', '<redacted>', '[REDACTED]']) { draft.password.value = marker; assert.throws(() => patchRequest(draft, baseline), { name: 'DraftError' }) }
  const reality = newDraft('vless')
  Object.assign(reality, { name: 'REALITY fixture', host: 'example.com', security: 'reality', serverName: 'example.com', fingerprint: 'chrome' })
  reality.uuid.value = randomUUID(); reality.publicKey.value = randomUUID()
  assert.equal(createRequest(reality).node.security.short_id, '')
  reality.shortID.mode = 'clear'
  assert.throws(() => createRequest(reality), { name: 'DraftError' })
})

test('switching security or authentication and unmount cleanup clears sensitive memory', () => {
  const draft = newDraft('vless')
  draft.publicKey.value = randomUUID(); draft.shortID.value = randomUUID(); draft.password.value = randomUUID()
  draft.security = 'tls'; resetSecurity(draft)
  assert.equal(draft.publicKey.value, ''); assert.equal(draft.shortID.value, '')
  resetAuthentication(draft); assert.equal(draft.password.value, '')
  draft.uuid.value = randomUUID(); clearDraftSecrets(draft); assert.equal(draft.uuid.value, '')
})

test('TLS optional fields distinguish omitted preservation, explicit clear and replacement', () => {
  const baseline = resource()
  baseline.node.protocol = 'trojan'; baseline.node.auth = { kind: 'password', has_password: true }
  baseline.node.security = { mode: 'tls', server_name: 'example.com', verify_certificate: true, alpn: ['h2'], client_fingerprint: 'chrome' }
  const draft = draftFromResource(baseline)
  const kept = patchRequest(draft, baseline).node.security
  assert.equal('alpn' in kept, false)
  assert.equal('client_fingerprint' in kept, false)
  draft.alpn = ''; draft.fingerprint = ''
  const cleared = patchRequest(draft, baseline).node.security
  assert.deepEqual(cleared.alpn, [])
  assert.equal(cleared.client_fingerprint, null)
  assert.equal(cleared.server_name, 'example.com')
  assert.equal(cleared.verify_certificate, true)
  draft.alpn = 'http/1.1'; draft.fingerprint = 'firefox'
  const replaced = patchRequest(draft, baseline).node.security
  assert.deepEqual(replaced.alpn, ['http/1.1'])
  assert.equal(replaced.client_fingerprint, 'firefox')
  assert.deepEqual(baseline.node.security.alpn, ['h2'])
  assert.equal(baseline.node.security.client_fingerprint, 'chrome')
})

test('REALITY allows optional ALPN clear but keeps its required fingerprint and secrets', () => {
  const baseline = resource()
  baseline.node.protocol = 'vless'; baseline.node.auth = { kind: 'uuid', has_uuid: true }
  baseline.node.security = { mode: 'reality', server_name: 'example.com', client_fingerprint: 'chrome', has_public_key: true, has_short_id: true, alpn: ['h2'] }
  const draft = draftFromResource(baseline)
  draft.alpn = ''
  const cleared = JSON.parse(JSON.stringify(patchRequest(draft, baseline))).node.security
  assert.deepEqual(cleared.alpn, [])
  assert.equal('client_fingerprint' in cleared, false)
  assert.equal('public_key' in cleared, false)
  assert.equal('short_id' in cleared, false)
  draft.fingerprint = ''
  assert.throws(() => patchRequest(draft, baseline), { name: 'DraftError' })
})
