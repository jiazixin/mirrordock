import { test, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'

test('overview timeline filters lanes by sync type and remembers the choice', async ({
  page,
  request,
}, testInfo) => {
  const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-overview-filter-'))
  const port = await new Promise<number>((resolve, reject) => {
    const server = net.createServer()
    server.on('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const value = (server.address() as net.AddressInfo).port
      server.close(() => resolve(value))
    })
  })
  const base = `http://127.0.0.1:${port}`
  const config = path.join(dir, 'config.yaml')
  await writeFile(
    config,
    `listen: 127.0.0.1\nport: ${port}\npath: ./state\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: overview-filter-fixture-password\nsources: []\n`,
  )
  const app = spawn(
    process.env.MIRRORDOCK_TEST_BINARY ?? path.resolve(import.meta.dirname, '../../bin/mirrordock'),
    ['-f', config],
    { stdio: 'ignore' },
  )
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  try {
    await expect
      .poll(async () => {
        try {
          return (await request.get(`${base}/api/auth/status`)).status()
        } catch {
          return 0
        }
      })
      .toBe(200)
    // Sources exist only in the mocked config response, so nothing is ever synced.
    const sources = [
      ['rocky', 'Rocky Linux', 'rsync'],
      ['alma', 'AlmaLinux', 'rsync'],
      ['epel', 'EPEL', 'reposync'],
      ['openlist', 'OpenList', 'github_release'],
      ['alist', 'Alist', 'github_release'],
      ['caddy', 'Caddy', 'github_release'],
    ].map(([id, name, type]) => ({
      id,
      name,
      type,
      dest: `fixture/${id}`,
      enabled: false,
      upstreams: {},
      schedule: { enabled: false, cron: '0 0 * * *' },
    }))
    await page.route('**/api/config', async (route) => {
      const response = await route.fetch()
      const payload = await response.json()
      payload.config.sources = sources
      await route.fulfill({ json: payload })
    })
    await page.route('**/api/events', (route) =>
      route.fulfill({ contentType: 'text/event-stream', body: '' }),
    )
    await page.route('**/api/runs?*', (route) => route.fulfill({ json: { items: [], total: 0 } }))
    await page.goto(`${base}/admin/overview`)
    await page.getByLabel('用户名', { exact: true }).fill('admin')
    await page.getByLabel('管理员密码').fill('overview-filter-fixture-password')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await expect(page.getByRole('heading', { name: '同步时间轴' })).toBeVisible()

    const lanes = page.locator('.lane:not(.lane-head)')
    const filter = page.getByRole('combobox', { name: '按同步方式筛选时间轴' })
    await expect(lanes).toHaveCount(6)
    await expect(filter).toContainText('全部类型 · 6')
    await filter.click()
    await expect(page.getByRole('option')).toHaveText([
      '全部类型 · 6',
      'Rsync · 2',
      'Reposync · 1',
      'GitHub Release · 3',
    ])
    await page.getByRole('option', { name: 'GitHub Release · 3' }).click()
    await expect(lanes).toHaveCount(3)
    await expect(lanes.locator('.lane-name strong')).toHaveText(['OpenList', 'Alist', 'Caddy'])
    // The KPI still counts every source.
    await expect(page.locator('.hero-stats article').first()).toContainText('6')

    await page
      .locator('.card')
      .first()
      .screenshot({ path: testInfo.outputPath('timeline.png') })

    await page.reload()
    await expect(filter).toContainText('GitHub Release · 3')
    await expect(lanes).toHaveCount(3)

    await filter.click()
    await page.getByRole('option', { name: 'Reposync · 1' }).click()
    await expect(lanes.locator('.lane-name strong')).toHaveText(['EPEL'])
    await filter.click()
    await page.getByRole('option', { name: '全部类型 · 6' }).click()
    await expect(lanes).toHaveCount(6)
    expect(await page.evaluate(() => localStorage.getItem('mirrordock.overview.type'))).toBeNull()

    // A single type needs no filter, and a stale remembered type falls back to all lanes.
    await page.evaluate(() => localStorage.setItem('mirrordock.overview.type', 'reposync'))
    sources.splice(2, 4)
    await page.reload()
    await expect(lanes).toHaveCount(2)
    await expect(filter).toHaveCount(0)
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
          }, 5000),
        ),
      ])
    }
    await rm(dir, { recursive: true, force: true })
  }
})
