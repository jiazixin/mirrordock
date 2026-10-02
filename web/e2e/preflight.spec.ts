import { test, expect, type Page, type APIRequestContext } from '@playwright/test'
import { spawn } from 'node:child_process'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'

async function fixture(page: Page, request: APIRequestContext) {
  const directory = await mkdtemp(path.join(tmpdir(), 'mirrordock-preflight-'))
  const port = await new Promise<number>((resolve, reject) => {
    const server = net.createServer()
    server.on('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const value = (server.address() as net.AddressInfo).port
      server.close(() => resolve(value))
    })
  })
  const base = `http://127.0.0.1:${port}`
  const configFile = path.join(directory, 'config.yaml')
  await writeFile(
    configFile,
    `listen: 127.0.0.1\nport: ${port}\npath: ./state\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: preflight-fixture-password\nsources: []\n`,
  )
  const app = spawn(
    process.env.MIRRORDOCK_TEST_BINARY ?? path.resolve(import.meta.dirname, '../../bin/mirrordock'),
    ['-f', configFile],
    { stdio: 'ignore' },
  )
  const close = async () => {
    if (app.exitCode === null) {
      app.kill('SIGTERM')
      await new Promise<void>((resolve) => {
        const timeout = setTimeout(() => app.kill('SIGKILL'), 5000)
        app.once('exit', () => {
          clearTimeout(timeout)
          resolve()
        })
      })
    }
    await rm(directory, { recursive: true, force: true })
  }
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
  } catch (error) {
    await close()
    throw error
  }
  const login = async () => {
    await page.goto(`${base}/admin/`)
    await page.getByLabel('管理员密码').fill('preflight-fixture-password')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await expect(page.locator('.page-heading')).toBeVisible()
  }
  return { base, login, close }
}

test('live detail follows new attempts, keeps historical selection and stops requests on close', async ({
  page,
  request,
}, testInfo) => {
  const server = await fixture(page, request)
  try {
    const now = new Date().toISOString()
    const run = {
      id: 1,
      source_id: 'fixture',
      name: '动态任务',
      type: 'rsync',
      state: 'queued',
      trigger: 'manual',
      created_at: now,
      elapsed_seconds: 0,
      budget_seconds: 30,
      progress: { phase: 'prepare', bytes: 0, success: 0, failed: 0, skipped: 0 },
      attempts: [] as {
        number: number
        candidate: string
        started_at: string
        ended_at?: string
      }[],
      events: [],
      snapshot: {},
    }
    await page.route('**/api/runs?*', (route) =>
      route.fulfill({ json: { items: [run], total: 1 } }),
    )
    await page.route('**/api/runs/1', (route) => route.fulfill({ json: run }))
    await page.route('**/api/runs/1/logs?*', (route) =>
      route.fulfill({
        json: {
          text: `日志尝试 ${new URL(route.request().url()).searchParams.get('attempt')}`,
          next_offset: 0,
        },
      }),
    )
    await page.route('**/api/events', (route) =>
      route.fulfill({ contentType: 'text/event-stream', body: '' }),
    )
    await server.login()
    await page.getByRole('link', { name: '运行记录', exact: true }).click()
    await page.getByRole('button', { name: '查看记录详情' }).click()
    await expect(page.getByRole('dialog')).toContainText('排队中')
    run.state = 'running'
    run.attempts.push({ number: 1, candidate: 'src1', started_at: now })
    await expect(page.getByRole('dialog')).toContainText('运行中')
    const attempts = page.getByRole('combobox', { name: '选择日志尝试' })
    await expect(attempts).toHaveText('尝试 #1')
    await expect(page.locator('.log-view')).toContainText('日志尝试 1')
    run.attempts.push({ number: 2, candidate: 'src2', started_at: now })
    await expect(attempts).toHaveText('尝试 #2')
    await attempts.click()
    await page.getByRole('option', { name: '尝试 #1', exact: true }).click()
    run.state = 'succeeded'
    run.attempts.push({ number: 3, candidate: 'src3', started_at: now, ended_at: now })
    await expect(page.getByRole('dialog').locator('.badge')).toHaveText('成功')
    await expect(attempts).toHaveText('尝试 #1')
    await page.screenshot({ path: testInfo.outputPath('live-detail.png'), fullPage: true })
    let requests = 0
    page.on('request', (req) => {
      if (req.url().includes('/api/runs/1')) requests++
    })
    await page.getByRole('button', { name: '关闭', exact: true }).click()
    const stopped = requests
    await page.waitForTimeout(3300)
    expect(requests).toBe(stopped)
  } finally {
    await server.close()
  }
})

test('switching source types ignores hidden required fields and modal traps focus', async ({
  page,
  request,
}) => {
  const server = await fixture(page, request)
  try {
    await server.login()
    await page.getByRole('button', { name: '新增同步源' }).first().click()
    const dialog = page.getByRole('dialog')
    await dialog.press('Shift+Tab')
    await expect(page.getByRole('button', { name: '保存并写回 YAML' })).toBeFocused()
    await page.getByLabel('源名称', { exact: true }).fill('Reposync 表单验证')
    await page.getByLabel('唯一标识').fill('repo-fixture')
    await page.getByLabel('目的目录 · dest').fill('repo-fixture')
    await page.getByRole('button', { name: '添加备用来源' }).click()
    await page.getByRole('combobox', { name: '同步方式', exact: true }).click()
    await page.getByRole('option', { name: 'DNF reposync' }).click()
    await dialog.locator('.upstream-row input').first().fill('https://example.com/repo/')
    await page.getByLabel('仓库 ID').fill('fixture')
    await page.getByRole('switch', { name: '检查包签名', exact: true }).uncheck()
    await page.getByRole('button', { name: '保存并写回 YAML' }).click()
    await expect(dialog).toHaveCount(0)
    const config = await (await page.request.get(`${server.base}/api/config`)).json()
    expect(config.config.sources[0].upstreams).toEqual({ src1: 'https://example.com/repo/' })
    expect(config.config.sources[0].type).toBe('reposync')
  } finally {
    await server.close()
  }
})

test('logout cannot be undone by a delayed config refresh', async ({ page, request }) => {
  const server = await fixture(page, request)
  let release: (() => void) | undefined
  try {
    await server.login()
    await page.route('**/api/config', async (route) => {
      const response = await route.fetch()
      await new Promise<void>((resolve) => {
        release = resolve
      })
      await route.fulfill({ response })
    })
    await page.getByRole('button', { name: '刷新', exact: true }).click()
    await expect.poll(() => !!release).toBe(true)
    await page.getByRole('button', { name: '退出登录' }).click()
    release!()
    await expect(page.getByLabel('管理员密码')).toBeVisible()
    await page.waitForTimeout(300)
    await expect(page.locator('.page-heading')).toHaveCount(0)
    await page.unroute('**/api/config')
    await page.getByLabel('管理员密码').fill('preflight-fixture-password')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await expect(page.locator('.page-heading')).toBeVisible()
  } finally {
    release?.()
    await server.close()
  }
})
