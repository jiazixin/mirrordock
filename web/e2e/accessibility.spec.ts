import AxeBuilder from '@axe-core/playwright'
import { test, expect, type Page } from '@playwright/test'
import { spawn } from 'node:child_process'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'

async function expectNoSeriousViolations(page: Page, where: string) {
  const audit = await new AxeBuilder({ page }).analyze()
  expect(
    audit.violations
      .filter((violation) => violation.impact === 'serious' || violation.impact === 'critical')
      .map((violation) => ({
        id: violation.id,
        nodes: violation.nodes.map(
          (node) => `${node.target.join(' ')}: ${node.failureSummary ?? ''}`,
        ),
      })),
    where,
  ).toEqual([])
}

async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = net.createServer()
    server.on('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const value = (server.address() as net.AddressInfo).port
      server.close(() => {
        resolve(value)
      })
    })
  })
}

/** Waits until no region is busy and every finite CSS animation has finished. */
async function settle(page: Page) {
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0)
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((animation) => animation.effect?.getComputedTiming().endTime !== Infinity)
        .map((animation) => animation.finished),
    ),
  )
}

const pages = [
  'overview',
  'sources',
  'batches',
  'records',
  'schedules',
  'analytics/rsync',
  'analytics/reposync',
  'analytics/github',
  'settings',
]

test('login and every admin page have no serious accessibility violations', async ({
  page,
  request,
}) => {
  const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-a11y-'))
  const port = await freePort()
  const base = `http://127.0.0.1:${port}`
  await writeFile(
    path.join(dir, 'config.yaml'),
    `listen: 127.0.0.1\nport: ${port}\npath: ./state\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: accessibility-fixture-password\nsources: []\n`,
  )
  const app = spawn(
    process.env.MIRRORDOCK_TEST_BINARY ?? path.resolve(import.meta.dirname, '../../bin/mirrordock'),
    ['-c', path.join(dir, 'config.yaml')],
    { stdio: 'ignore' },
  )
  try {
    await expect
      .poll(async () => {
        try {
          return (await request.get(`${base}/healthz`)).status()
        } catch {
          return 0
        }
      })
      .toBe(200)
    // Audit settled pages, not colors halfway through a transition.
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.goto(`${base}/admin/overview`)
    await expect(page.getByRole('button', { name: '登录管理台' })).toBeVisible()
    await settle(page)
    await expectNoSeriousViolations(page, 'login')
    await page.getByLabel('用户名', { exact: true }).fill('admin')
    await page.getByLabel('管理员密码').fill('accessibility-fixture-password')
    await page.getByRole('button', { name: '登录管理台' }).click()
    for (const route of pages) {
      await page.goto(`${base}/admin/${route}`)
      await expect(page.locator('main h1, h1').first()).toBeVisible()
      await settle(page)
      await expectNoSeriousViolations(page, route)
    }
  } finally {
    if (app.exitCode === null) {
      app.kill('SIGTERM')
      await new Promise((resolve) => app.once('exit', resolve))
    }
    await rm(dir, { recursive: true, force: true })
  }
})
