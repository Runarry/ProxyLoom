import { api, APIError } from './client'
import type { Schema } from './client'
import { terminal } from '../domain/test-result'

function pause(signal: AbortSignal, milliseconds: number) {
  return new Promise<void>(resolve => {
    const finish = () => { clearTimeout(timer); signal.removeEventListener('abort', finish); resolve() }
    const timer = setTimeout(finish, milliseconds)
    signal.addEventListener('abort', finish, { once: true })
    if (signal.aborted) finish()
  })
}

// Each reconnect restores the durable snapshot before resuming from the last
// consumed sequence. Closing this reader never sends a cancellation mutation.
export async function watchJob(id: string, signal: AbortSignal, handlers: {
  snapshot: (job: Schema<'Job'>) => void
  event: (event: Schema<'JobEvent'>) => void
  connection: (state: 'connected' | 'reconnecting') => void
  error: (error: unknown) => void
}) {
  let sequence = '0', backoff = 1000
  let connection: AbortController | undefined
  const reconnect = () => { connection?.abort(); handlers.connection('reconnecting') }
  window.addEventListener('offline', reconnect)
  window.addEventListener('online', reconnect)
  try {
  while (!signal.aborted) {
    if (!navigator.onLine) { handlers.connection('reconnecting'); await pause(signal, 1000); continue }
    connection = new AbortController()
    const requestSignal = AbortSignal.any([signal, connection.signal])
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
    try {
      handlers.connection('reconnecting')
      const current = await api<Schema<'JobResponse'>>(`/jobs/${id}`, { signal: requestSignal })
      if (signal.aborted) return
      handlers.snapshot(current.body.data)
      if (terminal(current.body.data.state)) return
      const response = await fetch(`/api/v1/jobs/${id}/events`, { signal: requestSignal, credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'text/event-stream', 'Last-Event-ID': sequence } })
      if (!response.ok || !response.body || !response.headers.get('Content-Type')?.startsWith('text/event-stream')) throw new APIError(response.status, 'SERVICE_UNAVAILABLE')
      handlers.connection('connected'); backoff = 1000
      reader = response.body.getReader()
      const decoder = new TextDecoder()
      let pending = '', reset = false
      while (!signal.aborted && !reset) {
        const chunk = await reader.read()
        if (chunk.done) break
        pending += decoder.decode(chunk.value, { stream: true }).replaceAll('\r', '')
        if (pending.length > 262144) throw new APIError(0, 'INVALID_RESPONSE')
        let end: number
        while ((end = pending.indexOf('\n\n')) >= 0) {
          const frame = pending.slice(0, end); pending = pending.slice(end + 2)
          const lines = frame.split('\n'), type = lines.find(line => line.startsWith('event:'))?.slice(6).trim()
          const data = lines.filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n')
          if (type === 'snapshot_reset') {
            const item = JSON.parse(data) as Schema<'SnapshotResetEvent'>
            if (item.job_id !== id || !/^(0|[1-9][0-9]*)$/.test(item.latest_seq)) throw new APIError(0, 'INVALID_RESPONSE')
            sequence = item.latest_seq; reset = true; break
          }
          if (type === 'job_event') {
            const event = JSON.parse(data) as Schema<'JobEvent'>
            if (event.job_id !== id || !/^[1-9][0-9]*$/.test(event.seq)) throw new APIError(0, 'INVALID_RESPONSE')
            if (BigInt(event.seq) > BigInt(sequence)) { sequence = event.seq; handlers.event(event) }
          }
        }
      }
    } catch (error) {
      if (signal.aborted) return
      if (!connection.signal.aborted) handlers.error(error)
      if (error instanceof APIError && [401, 403, 404].includes(error.status)) return
    } finally { await reader?.cancel().catch(() => {}); reader?.releaseLock() }
    if (!signal.aborted) { await pause(signal, backoff); backoff = Math.min(backoff * 2, 5000) }
  }
  } finally {
    connection?.abort()
    window.removeEventListener('offline', reconnect)
    window.removeEventListener('online', reconnect)
  }
}
