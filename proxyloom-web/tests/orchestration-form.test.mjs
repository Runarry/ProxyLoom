import { test } from 'node:test'
import assert from 'node:assert/strict'
import { newChainDraft, swapHops, chainCreateRequest, chainPatchRequest, chainDraftFromResource, newPolicyDraft, policyCreateRequest, policyPatchRequest, policyDraftFromResource, memberKey, removeMember, moveMember, enableHealthCheck } from '../src/domain/orchestration-form.ts'

const options = [
  { name: 'First', ref: { type: 'resource_ref', kind: 'node', resource_id: '10000000-0000-4000-8000-000000000001' } },
  { name: 'Exit', ref: { type: 'resource_ref', kind: 'node', resource_id: '10000000-0000-4000-8000-000000000002' } },
  { name: 'Chain', ref: { type: 'resource_ref', kind: 'chain', resource_id: '20000000-0000-4000-8000-000000000001' } },
]
const metadata = { name: 'Fixture', enabled: true, tags: [], revision: '9223372036854775806' }
function policyDraft() {
  return { ...newPolicyDraft(), name: metadata.name, members: options.map(option => ({ ...option.ref })), defaultMember: memberKey(options[0].ref) }
}

test('chain swap preserves order in stable references and rejects duplicate, absent and non-node hops', () => {
  const draft = { ...newChainDraft(), name: 'Ordered chain', hops: options.slice(0, 2).map(option => option.ref.resource_id) }
  swapHops(draft)
  assert.deepEqual(chainCreateRequest(draft, options).hops, [{ node_id: options[1].ref.resource_id }, { node_id: options[0].ref.resource_id }])
  draft.hops[1] = draft.hops[0]
  assert.throws(() => chainCreateRequest(draft, options), /两个不同/)
  draft.hops[1] = options[2].ref.resource_id
  assert.throws(() => chainCreateRequest(draft, options), /只允许引用已启用节点/)
  draft.hops[1] = options[0].ref.resource_id
  assert.throws(() => chainCreateRequest(draft, options.slice(1)), /只允许引用已启用节点/)
})

test('chain metadata patch omits unchanged hops and never mutates its baseline', () => {
  const baseline = { metadata, chain: { schema_version: 1, hops: options.slice(0, 2).map(option => ({ node_id: option.ref.resource_id })), failure_policy: 'fail_closed' } }
  const before = JSON.stringify(baseline)
  const draft = chainDraftFromResource(baseline)
  assert.throws(() => chainPatchRequest(draft, baseline, options), /没有需要保存/)
  draft.name = 'New name'
  assert.deepEqual(chainPatchRequest(draft, baseline, options), { name: 'New name' })
  swapHops(draft)
  assert.deepEqual(chainPatchRequest(draft, baseline, options).hops, [...baseline.chain.hops].reverse())
  assert.equal(JSON.stringify(baseline), before)
})

test('all policy strategies serialize explicit fail-closed members and a constrained default', () => {
  for (const strategy of ['fixed', 'manual_select', 'latency_best', 'round_robin']) {
    const draft = { ...policyDraft(), strategy }
    const body = policyCreateRequest(draft, options)
    assert.equal(body.policy_group.strategy, strategy)
    assert.equal(body.policy_group.on_unavailable, 'fail_closed')
    assert.deepEqual(body.policy_group.default_member, options[0].ref)
    assert.deepEqual(body.policy_group.health_check, { enabled: false })
  }
  const draft = policyDraft()
  moveMember(draft, 0, 1)
  assert.equal(draft.defaultMember, memberKey(options[0].ref))
  removeMember(draft, 1)
  assert.equal(draft.defaultMember, '')
  assert.throws(() => policyCreateRequest(draft, options), /默认成员/)
  draft.defaultMember = memberKey(draft.members[0])
  assert.throws(() => policyCreateRequest(draft, options.slice(0, 1)), /已启用节点或链路/)
  draft.members.push(draft.members[0])
  assert.throws(() => policyCreateRequest(draft, options), /重复成员/)
})

test('health checks validate contract bounds and omit hidden values after being disabled', () => {
  const draft = policyDraft()
  draft.health.enabled = true; enableHealthCheck(draft)
  draft.health.url = 'https://example.com/check'
  assert.deepEqual(policyCreateRequest(draft, options).policy_group.health_check, { enabled: true, url: 'https://example.com/check', interval_ms: 300000, timeout_ms: 5000, tolerance_ms: 50 })
  for (const url of ['http://example.com', 'https://user:password@example.com', 'https://example.com/#fragment', 'https://example.com:0/', 'https://example.com:99999/']) {
    draft.health.url = url
    assert.throws(() => policyCreateRequest(draft, options), { name: 'DraftError' })
  }
  draft.health.url = 'https://example.com/check'; draft.health.interval_ms = 999
  assert.throws(() => policyCreateRequest(draft, options), /检查间隔/)
  draft.health.enabled = false
  assert.deepEqual(policyCreateRequest(draft, options).policy_group.health_check, { enabled: false })
})

test('policy metadata and health patches omit schema_version and leave immutable baseline intact', () => {
  const draft = policyDraft()
  const baseline = { metadata, policy_group: policyCreateRequest(draft, options).policy_group, diagnostics: [] }
  const before = JSON.stringify(baseline)
  const editing = policyDraftFromResource(baseline)
  editing.name = 'Renamed'
  assert.deepEqual(policyPatchRequest(editing, baseline, options), { name: 'Renamed' })
  editing.health.enabled = true; enableHealthCheck(editing); editing.health.url = 'https://example.com/check'
  const body = policyPatchRequest(editing, baseline, options)
  assert.equal('schema_version' in body.policy_group, false)
  assert.equal('members' in body.policy_group, false)
  assert.equal(body.policy_group.health_check.enabled, true)
  assert.equal(JSON.stringify(baseline), before)
})
