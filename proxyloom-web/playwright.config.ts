import { defineConfig } from '@playwright/test'

if (!process.env.PROXYLOOM_E2E_BASE_URL) throw new Error('PROXYLOOM_E2E_BASE_URL must point to a real API serving the built web UI; E2E cannot be marked passed without it.')
if (!process.env.PROXYLOOM_E2E_USERNAME || !process.env.PROXYLOOM_E2E_PASSWORD) throw new Error('Set synthetic administrator PROXYLOOM_E2E_USERNAME and PROXYLOOM_E2E_PASSWORD for the isolated acceptance workspace.')

export default defineConfig({
  testDir: './tests/e2e', timeout: 90_000, expect: { timeout: 15_000 },
  fullyParallel: false, workers: 1, retries: 0, forbidOnly: true, reporter: [['list'], ['./tests/failure-reporter.mjs']], outputDir: 'test-results/e2e',
  use: { baseURL: process.env.PROXYLOOM_E2E_BASE_URL, channel: process.env.PROXYLOOM_E2E_BROWSER, headless: true, trace: 'off', screenshot: 'off', video: 'off' },
})
