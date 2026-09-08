import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests/ui', timeout: 45_000, expect: { timeout: 10_000 },
  fullyParallel: false, workers: 1, retries: 0, forbidOnly: true, reporter: [['list'], ['./tests/failure-reporter.mjs']], outputDir: 'test-results/ui',
  use: { baseURL: 'http://127.0.0.1:4173', channel: process.env.PROXYLOOM_E2E_BROWSER, headless: true, trace: 'off', screenshot: 'off', video: 'off' },
})
