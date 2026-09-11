import { test } from 'node:test'
import assert from 'node:assert/strict'
import { newDNS, newRouting, newRoutingRule, moveRule, routingRequest, dnsRequest, normalizedMatch, sameValue } from '../src/domain/network-draft.ts'

const id = '10000000-0000-4000-8000-000000000001'
const options = { targets: [{ name: 'Fixture', ref: { type: 'resource_ref', kind: 'node', resource_id: id } }], ruleSets: [{ id, name: 'Fixture rules' }] }
test('ordered route normalization preserves match grouping and rejects absent resources or empty rules', () => {
  const draft = newRouting()
  draft.rules.push({ ...newRoutingRule(), comment: 'first', match: { domain_suffix: ['EXAMPLE.com', 'example.com'], ip_cidrs: ['2001:DB8::/32'], network: ['tcp', 'udp'] }, action: options.targets[0].ref })
  draft.rules.push({ ...newRoutingRule(), comment: 'second', match: { destination_ports: [{ from: 443, to: 443 }], rule_set_ids: [id] } })
  const before = JSON.stringify(draft)
  const normalized = routingRequest(draft, options)
  assert.deepEqual(normalized.rules[0].match, { domain_suffix: ['example.com'], ip_cidrs: ['2001:db8::/32'], network: ['tcp', 'udp'] })
  assert.equal(JSON.stringify(draft), before)
  moveRule(draft.rules, 1, -1)
  assert.deepEqual(routingRequest(draft, options).rules.map(rule => rule.comment), ['second', 'first'])
  assert.throws(() => routingRequest(draft, { targets: [], ruleSets: options.ruleSets }), /资源未启用/)
  draft.rules[0].match = {}
  assert.throws(() => routingRequest(draft, options), /至少需要一个/)
})
test('domain and route matches omit empty optional fields, validate ports and do not broaden an empty match', () => {
  assert.deepEqual(normalizedMatch({ domain_exact: ['例子.COM'], domain_suffix: [], network: [] }, options), { domain_exact: ['xn--fsqu00a.com'] })
  assert.throws(() => normalizedMatch({ destination_ports: [{ from: 10, to: 2 }] }, options), /起始端口/)
  assert.throws(() => normalizedMatch({ domain_exact: [] }, options), /至少需要一个/)
})
test('DNS validates stable resolver references, literal IP addresses and explicit outbound choices', () => {
  const draft = newDNS()
  draft.bootstrap.push({ resolver_id: 'bootstrap_udp', kind: 'udp', address: '2001:DB8::53', port: 53 })
  draft.resolvers.push({ resolver_id: 'https', kind: 'https', url: 'https://example.com/dns-query', bootstrap_resolver_id: 'bootstrap_udp', outbound: options.targets[0].ref })
  draft.final_resolver = 'https'
  draft.rules.push({ enabled: true, comment: '', match: { domain_suffix: ['EXAMPLE.COM'] }, resolver_id: 'local' })
  const before = JSON.stringify(draft)
  const body = dnsRequest(draft, options)
  assert.equal(body.bootstrap[1].address, '2001:db8::53')
  assert.deepEqual(body.rules[0].match, { domain_suffix: ['example.com'] })
  assert.equal(JSON.stringify(draft), before)
  draft.resolvers[1].bootstrap_resolver_id = 'missing'
  assert.throws(() => dnsRequest(draft, options), /引导解析器/)
  draft.final_resolver = 'missing'
  assert.throws(() => dnsRequest(draft, options), /主解析器/)
  draft.resolvers[1].resolver_id = 'bootstrap'
  assert.throws(() => dnsRequest(draft, options), /ID 必须唯一/)
})
test('semantic comparison ignores JSON object-key order while detecting rule order changes', () => {
  const first = { match: { domain_exact: ['example.com'], network: ['tcp'] }, enabled: true, comment: '' }
  const second = { enabled: true, match: { network: ['tcp'], domain_exact: ['example.com'] }, comment: '' }
  assert.equal(sameValue(first, second), true)
  assert.equal(sameValue([first, {}], [{}, second]), false)
  assert.equal(sameValue(first, { ...second, enabled: false }), false)
})
