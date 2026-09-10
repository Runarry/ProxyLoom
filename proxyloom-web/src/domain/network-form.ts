import type { Schema } from '../api/client'

export class NetworkFormError extends Error {
  readonly line?: number
  constructor(message: string, line?: number) {
    super(line === undefined ? message : `第 ${line} 行：${message}`)
    this.name = 'NetworkFormError'
    this.line = line
  }
}

export function normalizeDomain(input: string): string {
  const value = input.trim().replace(/\.$/, '')
  if (!value || /[\s\x00-\x1f\x7f/@:#?%\\*\[\],]/u.test(value)) throw new NetworkFormError('请输入完整域名，不要包含协议、端口、通配符或路径。')
  let domain: string
  try { domain = new URL(`http://${value}`).hostname.toLowerCase() } catch { throw new NetworkFormError('域名格式无效。') }
  if (domain.length > 253 || domain.split('.').some(label => !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))) throw new NetworkFormError('域名格式无效或超过长度限制。')
  // WHATWG URL rewrites numeric IPv4 aliases; these are not domain spelling normalization.
  if (/^[0-9.]+$/.test(value) && domain !== value) throw new NetworkFormError('请使用完整域名或在 CIDR 规则中填写标准 IP 地址。')
  return domain
}

function ipv4Bytes(input: string): number[] {
  const parts = input.split('.')
  if (parts.length !== 4 || parts.some(part => !/^(?:0|[1-9]\d{0,2})$/.test(part) || Number(part) > 255)) throw new NetworkFormError('IPv4 地址必须包含四个 0–255 的十进制段。')
  return parts.map(Number)
}

function ipv6Words(input: string): number[] {
  if (!/^[0-9a-fA-F:.]+$/.test(input)) throw new NetworkFormError('IPv6 地址格式无效。')
  let value = input.toLowerCase()
  if (value.includes('.')) {
    const lastColon = value.lastIndexOf(':')
    if (lastColon < 0) throw new NetworkFormError('IPv6 地址格式无效。')
    const bytes = ipv4Bytes(value.slice(lastColon + 1))
    value = value.slice(0, lastColon + 1) + ((bytes[0] << 8) | bytes[1]).toString(16) + ':' + ((bytes[2] << 8) | bytes[3]).toString(16)
  }
  const halves = value.split('::')
  if (halves.length > 2) throw new NetworkFormError('IPv6 地址只能包含一次双冒号。')
  const left = halves[0] ? halves[0].split(':') : []
  const right = halves.length === 2 && halves[1] ? halves[1].split(':') : []
  const words = [...left, ...right]
  if (words.some(word => !/^[0-9a-f]{1,4}$/.test(word)) || (halves.length === 1 ? words.length !== 8 : words.length >= 8)) throw new NetworkFormError('IPv6 地址格式无效。')
  return [...left.map(word => Number.parseInt(word, 16)), ...Array(halves.length === 2 ? 8 - words.length : 0).fill(0), ...right.map(word => Number.parseInt(word, 16))]
}

function formatIPv6(words: number[]): string {
  let bestStart = -1, bestLength = 1
  for (let i = 0; i < words.length;) {
    if (words[i] !== 0) { i++; continue }
    let end = i
    while (end < words.length && words[end] === 0) end++
    if (end - i > bestLength) { bestStart = i; bestLength = end - i }
    i = end
  }
  const hex = words.map(word => word.toString(16))
  if (bestStart < 0) return hex.join(':')
  return hex.slice(0, bestStart).join(':') + '::' + hex.slice(bestStart + bestLength).join(':')
}

export function normalizeCIDR(input: string): string {
  const value = input.trim()
  const parts = value.split('/')
  if (parts.length !== 2 || !/^(?:0|[1-9]\d{0,2})$/.test(parts[1])) throw new NetworkFormError('网段必须采用 IP/前缀长度格式。')
  const v6 = parts[0].includes(':')
  const prefix = Number(parts[1])
  if (prefix > (v6 ? 128 : 32)) throw new NetworkFormError('网段前缀长度超出地址范围。')
  const words = v6 ? ipv6Words(parts[0]) : []
  const bytes = v6 ? words.flatMap(word => [word >> 8, word & 255]) : ipv4Bytes(parts[0])
  for (let i = 0; i < bytes.length; i++) {
    const kept = Math.min(8, Math.max(0, prefix - i * 8))
    if ((bytes[i] & (255 >> kept)) !== 0) throw new NetworkFormError('网段包含非零主机位，请填写该网段的网络地址。')
  }
  // netip.String preserves the familiar IPv4-mapped spelling.
  const mapped = v6 && words.slice(0, 5).every(word => word === 0) && words[5] === 0xffff
  const address = mapped ? `::ffff:${bytes.slice(12).join('.')}` : v6 ? formatIPv6(words) : bytes.join('.')
  return `${address}/${prefix}`
}

export type ParsedRuleSet = { entries: Schema<'RuleSetEntry'>[]; lineNumbers: number[] }

export function parseRuleSetText(text: string): ParsedRuleSet {
  if (new TextEncoder().encode(text).byteLength > 10 * 1024 * 1024) throw new NetworkFormError('规则文本不能超过 10 MiB。')
  const result: ParsedRuleSet = { entries: [], lineNumbers: [] }
  for (const [index, raw] of text.split(/\r?\n/).entries()) {
    const line = raw.trim()
    if (!line || line.startsWith('#')) continue
    try {
      const parts = line.split(',').map(part => part.trim())
      if (parts.length > 2 || parts.some(part => !part)) throw new NetworkFormError('每行只能包含一种规则类型和一个值。')
      const kind = parts.length === 2 ? parts[0].toUpperCase() : parts[0].includes('/') ? 'IP-CIDR' : 'DOMAIN'
      const value = parts.at(-1)!
      let entry: Schema<'RuleSetEntry'>
      if (kind === 'DOMAIN' || kind === 'DOMAIN-SUFFIX') entry = { kind: 'domain', domain: normalizeDomain(value), match: kind === 'DOMAIN' ? 'exact' : 'suffix' }
      else if (kind === 'IP-CIDR' || kind === 'IP-CIDR6') {
        const cidr = normalizeCIDR(value)
        if (kind === 'IP-CIDR6' && !cidr.includes(':')) throw new NetworkFormError('IP-CIDR6 规则需要 IPv6 地址。')
        entry = { kind: 'cidr', cidr }
      } else throw new NetworkFormError('仅支持 DOMAIN、DOMAIN-SUFFIX、IP-CIDR 和 IP-CIDR6 文本规则。')
      result.entries.push(entry)
      result.lineNumbers.push(index + 1)
      if (result.entries.length > 50000) throw new NetworkFormError('规则集最多包含 50,000 条规则。')
    } catch (error) {
      if (error instanceof NetworkFormError) throw new NetworkFormError(error.message, index + 1)
      throw error
    }
  }
  if (!result.entries.length) throw new NetworkFormError('规则集至少需要一条域名或网段规则。')
  return result
}

export function formatRuleSetText(entries: Schema<'RuleSetEntry'>[]): string {
  return entries.map(entry => entry.kind === 'domain' ? `${entry.match === 'exact' ? 'DOMAIN' : 'DOMAIN-SUFFIX'},${entry.domain}` : `${entry.cidr.includes(':') ? 'IP-CIDR6' : 'IP-CIDR'},${entry.cidr}`).join('\n')
}

export function ruleSetErrorLine(fieldPath: string, lineNumbers: number[]): number | undefined {
  const match = fieldPath.match(/(?:^|\/)entries\/(0|[1-9]\d*)(?:\/|$)/)
  return match ? lineNumbers[Number(match[1])] : undefined
}

export function parsePortRanges(text: string): Schema<'PortRange'>[] {
  if (!text.trim()) return []
  const ranges = text.split(/[,，\s]+/u).filter(Boolean).map(value => {
    if (!/^\d{1,5}(?:-\d{1,5})?$/.test(value)) throw new NetworkFormError('端口应为单个数字或起止范围，例如 443、8000-8100。')
    const [from, end] = value.split('-').map(Number)
    const to = end ?? from
    if (from < 1 || to > 65535 || from > to) throw new NetworkFormError('端口范围必须位于 1–65535，且起点不大于终点。')
    return { from, to }
  })
  if (ranges.length > 100) throw new NetworkFormError('每条路由最多包含 100 个端口范围。')
  return ranges
}
