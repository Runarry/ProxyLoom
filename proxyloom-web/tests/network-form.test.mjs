import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizeDomain, normalizeCIDR, parseRuleSetText, formatRuleSetText, ruleSetErrorLine, parsePortRanges, NetworkFormError } from '../src/domain/network-form.ts'

test('domain normalization preserves suffix intent and handles IDNA without accepting URLs', () => {
  assert.equal(normalizeDomain(' Example.COM. '), 'example.com')
  assert.equal(normalizeDomain('例子.测试'), 'xn--fsqu00a.xn--0zwm56d')
  for (const value of ['https://example.com', '*.example.com', 'a@b.com', 'a%2eb.com', 'a b.com', 'a..com', '-bad.com', '127.1']) assert.throws(() => normalizeDomain(value), NetworkFormError)
})

test('CIDR canonicalization keeps IPv4 and IPv6 networks without widening host addresses', () => {
  for (const [input, expected] of [['192.0.2.0/24', '192.0.2.0/24'], ['2001:0DB8:0000:0000:0000:0000:0000:0000/32', '2001:db8::/32'], ['::/0', '::/0'], ['::ffff:192.0.2.0/120', '::ffff:192.0.2.0/120'], ['2001:db8:0:1:0:0:0:1/128', '2001:db8:0:1::1/128']]) assert.equal(normalizeCIDR(input), expected)
  for (const invalid of ['192.0.2.3/24', '2001:db8::1/64', '192.0.2.0/33', '::/129', '2001::db8::/32', '192.00.2.0/24', 'example.com/24', '::%lo/128', '[::]/128']) assert.throws(() => normalizeCIDR(invalid), NetworkFormError)
})

test('text entries retain original line numbers across comments and blank lines', () => {
  const parsed = parseRuleSetText('# comment\n\nDOMAIN,Example.COM\nDOMAIN-SUFFIX,例子.测试\nIP-CIDR,192.0.2.0/24\nIP-CIDR6,2001:db8::/32')
  assert.deepEqual(parsed.lineNumbers, [3, 4, 5, 6])
  assert.deepEqual(parsed.entries[0], { kind: 'domain', domain: 'example.com', match: 'exact' })
  assert.equal(parsed.entries[1].match, 'suffix')
  assert.deepEqual(parseRuleSetText(formatRuleSetText(parsed.entries)).entries, parsed.entries)
  assert.equal(ruleSetErrorLine('/rule_set/entries/2/cidr', parsed.lineNumbers), 5)
  assert.equal(ruleSetErrorLine('/entries/0/domain', parsed.lineNumbers), 3)
  assert.equal(ruleSetErrorLine('/name', parsed.lineNumbers), undefined)
  assert.throws(() => parseRuleSetText('# comment\n\nIP-CIDR,192.0.2.3/24'), error => error.line === 3 && !error.message.includes('192.0.2.3'))
})

test('text rules reject ambiguous native actions, unsupported binary grammar and empty input', () => {
  for (const text of ['DOMAIN,example.com,DIRECT', 'GEOIP,CN', 'MATCH,DIRECT', '+.example.com', '# comments only', 'IP-CIDR6,192.0.2.0/24', 'DOMAIN,']) assert.throws(() => parseRuleSetText(text), NetworkFormError)
  assert.deepEqual(parseRuleSetText('example.com\n192.0.2.0/24').entries, [{ kind: 'domain', domain: 'example.com', match: 'exact' }, { kind: 'cidr', cidr: '192.0.2.0/24' }])
})

test('port lists preserve OR alternatives and reject reversed or invalid ranges', () => {
  assert.deepEqual(parsePortRanges('443, 8000-8100，53'), [{ from: 443, to: 443 }, { from: 8000, to: 8100 }, { from: 53, to: 53 }])
  assert.deepEqual(parsePortRanges(''), [])
  for (const text of ['0', '65536', '20-10', '-1', '80-http']) assert.throws(() => parsePortRanges(text), NetworkFormError)
})
