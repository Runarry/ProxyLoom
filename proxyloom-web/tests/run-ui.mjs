import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { setTimeout as delay } from 'node:timers/promises'

// Own both direct Node children. Avoid platform shell trees and Playwright's Windows taskkill teardown.
const server = spawn(process.execPath, ['node_modules/vite/bin/vite.js', '--host', '127.0.0.1', '--port', '4173', '--strictPort'], { stdio: ['ignore', 'pipe', 'inherit'], windowsHide: true })
let bound = false
server.stdout.on('data', chunk => {
  process.stdout.write(chunk)
  if (chunk.toString().replace(/\u001b\[[0-9;]*m/g, '').includes('http://127.0.0.1:4173/')) bound = true
})
let runner
const stop = () => { runner?.kill(); server.kill() }
process.on('SIGINT', stop)
process.on('SIGTERM', stop)
try {
  let ready = false
  for (let attempt = 0; attempt < 100; attempt++) {
    if (server.exitCode !== null) throw new Error('The isolated UI development server exited before becoming ready.')
    try { if (bound && (await fetch('http://127.0.0.1:4173')).ok) { ready = true; break } } catch { /* start-up window */ }
    await delay(200)
  }
  if (!ready) throw new Error('The isolated UI development server did not become ready.')
  runner = spawn(process.execPath, ['node_modules/@playwright/test/cli.js', 'test', '--config', 'playwright.ui.config.ts', ...process.argv.slice(2)], { stdio: 'inherit', windowsHide: true })
  const [code] = await once(runner, 'exit')
  process.exitCode = code ?? 1
} finally {
  stop()
}
