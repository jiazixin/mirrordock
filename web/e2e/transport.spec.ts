import { defined } from './support'
import { test, expect } from '@playwright/test'
import type { APIRequestContext, Page } from '@playwright/test'
import { execFileSync, spawn } from 'node:child_process'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'

// Browsers treat 127.0.0.1 as a secure context even over plain HTTP. Resolving a LAN-style name
// to the loopback server reproduces an HTTP console opened from another machine: no secure
// context, no Sec-Fetch-Site header and no Secure cookie.
const lanHost = 'mirror.lan.test'
test.use({
  launchOptions: { args: [`--host-resolver-rules=MAP ${lanHost} 127.0.0.1`] },
  ignoreHTTPSErrors: true,
})

const password = 'transport-fixture-password'

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const listener = net.createServer()
    listener.on('error', reject)
    listener.listen(0, '127.0.0.1', () => {
      const port = (listener.address() as net.AddressInfo).port
      listener.close(() => {
        resolve(port)
      })
    })
  })
}

type Transport = { name: string; tls: boolean; secureContext: boolean }

async function exerciseConsole(page: Page, request: APIRequestContext, transport: Transport) {
  const dir = await mkdtemp(path.join(tmpdir(), `mirrordock-transport-${transport.name}-`))
  const port = await freePort()
  const scheme = transport.tls ? 'https' : 'http'
  const loopback = `${scheme}://127.0.0.1:${port}`
  const base = transport.secureContext ? loopback : `${scheme}://${lanHost}:${port}`
  let tls = ''
  if (transport.tls) {
    execFileSync(
      'openssl',
      [
        ...['req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:prime256v1', '-nodes'],
        ...['-keyout', path.join(dir, 'key.pem'), '-out', path.join(dir, 'cert.pem')],
        ...['-days', '1', '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1'],
      ],
      { stdio: 'ignore' },
    )
    tls = 'tls:\n  cert: ./cert.pem\n  key: ./key.pem\n'
  }
  // The upstream port is closed: the run is only queued here, never synchronized.
  const source =
    '  - id: fixture\n    name: Fixture\n    type: rsync\n    enabled: true\n    dest: fixture\n' +
    '    src1: rsync://127.0.0.1:1/fixture/\n    schedule: {enabled: false, cron: "", overlap: skip}\n'
  await writeFile(
    path.join(dir, 'config.yaml'),
    `listen: 127.0.0.1\nport: ${port}\npath: ./state\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: ${password}\n${tls}settings:\n  retry_count: 0\nsources:\n${source}`,
  )
  const app = spawn(
    process.env.MIRRORDOCK_TEST_BINARY ?? path.resolve(import.meta.dirname, '../../bin/mirrordock'),
    ['-c', path.join(dir, 'config.yaml')],
    { stdio: 'ignore' },
  )
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  try {
    await expect
      .poll(async () => {
        try {
          return (await request.get(`${loopback}/healthz`)).status()
        } catch {
          return 0
        }
      })
      .toBe(200)
    const events = page.waitForResponse(
      (response) => response.url() === `${base}/api/events` && response.status() === 200,
    )
    await page.goto(`${base}/admin/settings`)
    expect(await page.evaluate(() => window.isSecureContext)).toBe(transport.secureContext)
    await page.getByLabel('用户名', { exact: true }).fill('admin')
    await page.getByLabel('管理员密码').fill(password)
    await page.getByRole('button', { name: '登录管理台' }).click()
    await expect(page.getByRole('heading', { name: '系统设置' })).toBeVisible()
    await events

    // The session cookie is scoped to the API path.
    const cookies = await page.context().cookies(`${base}/api/`)
    const session = defined(
      cookies.find((cookie) => cookie.name === 'mirrordock_session'),
      'session cookie',
    )
    expect(session.secure).toBe(transport.tls)
    expect(session.httpOnly).toBe(true)
    expect(session.sameSite).toBe('Strict')

    // A conditional write and a keyed creation both pass the cross-site check.
    const cards = page.locator('.toast-card')
    await page.getByRole('button', { name: '保存设置', exact: true }).click()
    await expect(cards.getByRole('status').filter({ hasText: '配置已写回 YAML' })).toBeVisible()
    await page.goto(`${base}/admin/sources`)
    await page.getByRole('button', { name: '同步 Fixture' }).click()
    await expect(cards.getByRole('status').filter({ hasText: '同步任务已加入队列' })).toBeVisible()

    // The public directory and its link back to the console use the same origin.
    await page.goto(`${base}/`)
    await expect(page.getByRole('link', { name: '管理后台' })).toBeVisible()
    expect(errors).toEqual([])
  } finally {
    if (app.exitCode === null) {
      app.kill('SIGTERM')
      await Promise.race([
        new Promise((resolve) => app.once('exit', resolve)),
        new Promise((resolve) =>
          setTimeout(() => {
            app.kill('SIGKILL')
            resolve(null)
          }, 15000),
        ),
      ])
    }
    await rm(dir, { recursive: true, force: true })
  }
}

test('console works over plain HTTP opened by a LAN name', async ({ page, request }) => {
  await exerciseConsole(page, request, { name: 'http', tls: false, secureContext: false })
})

test('console works over native HTTPS', async ({ page, request }) => {
  await exerciseConsole(page, request, { name: 'https', tls: true, secureContext: true })
})
