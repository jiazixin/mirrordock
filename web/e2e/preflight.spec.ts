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
    await page.goto(`${base}/admin/sources`)
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
    await page.locator('summary').filter({ hasText: '原始日志' }).click()
    run.state = 'running'
    await expect(page.getByRole('dialog')).toContainText('运行中')
    // The first attempt can arrive after preparation, without a state change.
    run.attempts.push({ number: 1, candidate: 'src1', started_at: now })
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
    await page.getByRole('option', { name: 'DNF Reposync' }).click()
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

function detailRun(state: string) {
  const now = new Date().toISOString()
  return {
    id: 1,
    source_id: 'fixture',
    name: '详情读取生命周期',
    type: 'rsync',
    state,
    trigger: 'manual',
    created_at: now,
    started_at: now,
    elapsed_seconds: 10,
    budget_seconds: 60,
    progress: {
      phase: 'transfer',
      bytes: 100,
      received_bytes: null,
      success: 0,
      failed: 0,
      skipped: 0,
    },
    attempts: [{ number: 1, candidate: 'rsync://fixture.example/repository/', started_at: now }],
    events: [],
    snapshot: {},
  }
}

test('terminal detail drains a bounded log backlog then stops and preserves disclosure focus', async ({
  page,
  request,
}) => {
  const server = await fixture(page, request)
  try {
    const run = detailRun('succeeded')
    let detailReads = 0,
      logReads = 0
    const content = Buffer.from('x'.repeat(17 * 128 * 1024) + '终态日志尾部')
    await page.route('**/api/runs?*', (route) =>
      route.fulfill({ json: { items: [run], total: 1 } }),
    )
    await page.route('**/api/runs/1', (route) => {
      detailReads++
      return route.fulfill({ json: run })
    })
    await page.route('**/api/events', (route) =>
      route.fulfill({ contentType: 'text/event-stream', body: '' }),
    )
    await page.route('**/api/runs/1/logs?*', (route) => {
      logReads++
      const offset = Number(new URL(route.request().url()).searchParams.get('offset'))
      const next = Math.min(content.length, offset + 128 * 1024)
      return route.fulfill({
        json: { text: content.subarray(offset, next).toString(), next_offset: next },
      })
    })
    await server.login()
    await page.getByRole('link', { name: '运行记录', exact: true }).click()
    await page.getByRole('button', { name: '查看记录详情' }).click()
    await expect(page.getByRole('dialog')).toContainText('自动刷新已停止')
    expect(logReads).toBe(0)
    const logs = page.locator('summary').filter({ hasText: '原始日志' })
    await logs.focus()
    await page.keyboard.press('Enter')
    await expect(page.locator('.log-view')).toContainText('终态日志尾部', { timeout: 12000 })
    expect(logReads).toBe(18)
    await page.waitForTimeout(3300)
    expect(logReads).toBe(18)
    expect(detailReads).toBe(1)
    await page.getByRole('button', { name: '刷新运行详情' }).click()
    await expect.poll(() => detailReads).toBe(2)
    const configuration = page.locator('summary').filter({ hasText: '配置快照与事件' })
    await configuration.focus()
    await page.keyboard.press('Tab')
    await expect(page.getByRole('button', { name: '关闭', exact: true })).toBeFocused()
  } finally {
    await server.close()
  }
})

test('detail pauses hidden and folded reads, resumes, and reads after in-flight terminal transition', async ({
  page,
  request,
}) => {
  const server = await fixture(page, request)
  let release: (() => void) | undefined
  try {
    const run = detailRun('running')
    let detailReads = 0,
      logReads = 0,
      hold = false
    await page.route('**/api/runs?*', (route) =>
      route.fulfill({ json: { items: [run], total: 1 } }),
    )
    await page.route('**/api/runs/1', (route) => {
      detailReads++
      return route.fulfill({ json: run })
    })
    await page.route('**/api/events', (route) =>
      route.fulfill({ contentType: 'text/event-stream', body: '' }),
    )
    await page.route('**/api/runs/1/logs?*', async (route) => {
      logReads++
      const terminal = run.state === 'succeeded'
      if (hold) {
        hold = false
        await new Promise<void>((resolve) => {
          release = resolve
        })
        release = undefined
      }
      return route.fulfill({
        json: { text: terminal ? '最终收尾日志' : '活动日志', next_offset: 0 },
      })
    })
    await server.login()
    await page.getByRole('link', { name: '运行记录', exact: true }).click()
    await page.getByRole('button', { name: '查看记录详情' }).click()
    const logs = page.locator('summary').filter({ hasText: '原始日志' })
    await logs.click()
    await expect(page.locator('.log-view')).toContainText('活动日志')
    await logs.click()
    const foldedReads = logReads
    await page.waitForTimeout(3300)
    expect(logReads).toBe(foldedReads)
    await logs.click()
    await expect(page.locator('.log-view')).toContainText('活动日志')
    await page.evaluate(() => {
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
      document.dispatchEvent(new Event('visibilitychange'))
    })
    const hiddenReads = [detailReads, logReads]
    await page.waitForTimeout(3300)
    expect([detailReads, logReads]).toEqual(hiddenReads)
    await page.evaluate(() => {
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
      document.dispatchEvent(new Event('visibilitychange'))
    })
    await expect.poll(() => detailReads).toBeGreaterThan(hiddenReads[0])
    await expect(page.getByRole('button', { name: '刷新日志', exact: true })).toBeEnabled()
    hold = true
    await page.getByRole('button', { name: '刷新日志', exact: true }).click()
    await expect.poll(() => !!release).toBe(true)
    run.state = 'succeeded'
    await page.getByRole('button', { name: '刷新运行详情' }).click()
    await expect(page.getByRole('dialog').locator('.badge')).toHaveText('成功')
    release!()
    await expect(page.locator('.log-view')).toContainText('最终收尾日志', { timeout: 12000 })
    const finalReads = [detailReads, logReads]
    await page.waitForTimeout(3300)
    expect([detailReads, logReads]).toEqual(finalReads)
  } finally {
    release?.()
    await server.close()
  }
})

test('old session requests cannot expire a new login', async ({ page, request }) => {
  const server = await fixture(page, request)
  let release: (() => void) | undefined
  let held = false
  try {
    await server.login()
    await page.route('**/api/system/checks', async (route) => {
      if (held) return route.continue()
      held = true
      await new Promise<void>((resolve) => {
        release = resolve
      })
      await route.fulfill({ status: 401, json: { error: 'old session' } }).catch(() => {})
    })
    await page.getByRole('link', { name: '系统设置', exact: true }).click()
    await page.getByRole('tab', { name: '系统配置', exact: true }).click()
    await expect.poll(() => !!release).toBe(true)
    await page.getByRole('button', { name: '退出登录' }).click()
    await expect(page.getByLabel('管理员密码')).toBeVisible()
    await page.getByLabel('管理员密码').fill('preflight-fixture-password')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await expect(page.locator('.page-heading')).toBeVisible()
    await page.unroute('**/api/system/checks')
    release!()
    await page.waitForTimeout(300)
    await expect(page.locator('.page-heading')).toBeVisible()
    await expect(page.getByLabel('管理员密码')).toHaveCount(0)
  } finally {
    release?.()
    await server.close()
  }
})

test('batch run controls ask before pausing, resuming, cancelling or rerunning', async ({
  page,
  request,
}) => {
  const server = await fixture(page, request)
  try {
    const now = new Date().toISOString()
    const base = {
      type: 'rsync',
      trigger: 'manual',
      created_at: now,
      elapsed_seconds: 0,
      budget_seconds: 30,
      progress: {
        phase: 'transfer',
        bytes: 0,
        percent: null,
        success: 0,
        failed: 0,
        skipped: 0,
      },
      attempts: [],
      events: [],
      snapshot: {},
    }
    const runs = [
      { ...base, id: 1, source_id: 'live', name: '运行任务', state: 'running' },
      { ...base, id: 2, source_id: 'held', name: '暂停任务源', state: 'paused' },
      { ...base, id: 3, source_id: 'done', name: '失败任务', state: 'failed' },
    ]
    const posted: string[] = []
    await page.route('**/api/runs?*', (route) =>
      route.fulfill({ json: { items: runs, total: runs.length } }),
    )
    await page.route(/\/api\/(runs\/\d+\/\w+|sources\/[^/]+\/run)$/, (route) => {
      posted.push(new URL(route.request().url()).pathname)
      return route.fulfill({ json: {} })
    })
    await page.route('**/api/events', (route) =>
      route.fulfill({ contentType: 'text/event-stream', body: '' }),
    )
    await server.login()
    await page.getByRole('link', { name: '批次记录', exact: true }).click()
    const native: string[] = []
    page.on('dialog', async (dialog) => {
      native.push(dialog.message())
      await dialog.dismiss()
    })
    const confirm = page.getByRole('alertdialog')
    const controls = [
      ['暂停任务', '暂停“运行任务”', '暂停', '/api/runs/1/pause'],
      ['继续任务', '继续“暂停任务源”', '继续', '/api/runs/2/resume'],
      ['重新同步', '重新同步“失败任务”', '重新同步', '/api/sources/done/run'],
    ] as const
    for (const [name, prompt, label, endpoint] of controls) {
      for (const accept of [false, true]) {
        await page.getByRole('button', { name, exact: true }).click()
        await expect(confirm).toBeVisible()
        await expect(confirm).toHaveAccessibleName(name)
        await expect(confirm).toContainText(prompt)
        await confirm.getByRole('button', { name: accept ? label : '取消', exact: true }).click()
        await expect(confirm).toHaveCount(0)
        if (accept) await expect.poll(() => posted.at(-1)).toBe(endpoint)
        else expect(posted).not.toContain(endpoint)
      }
    }
    const cancel = page.getByRole('button', { name: '取消任务', exact: true }).first()
    await cancel.click()
    await expect(confirm).toContainText('取消后无法继续')
    // Destructive confirmations start on the safe choice.
    await expect(confirm.getByRole('button', { name: '保留任务' })).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(confirm.getByRole('button', { name: '取消任务' })).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(confirm.getByRole('button', { name: '保留任务' })).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(confirm).toHaveCount(0)
    await expect(cancel).toBeFocused()
    await cancel.click()
    await page.mouse.click(5, 5)
    await expect(confirm).toHaveCount(0)
    await page.waitForTimeout(300)
    expect(posted.filter((path) => path.endsWith('/cancel'))).toEqual([])
    expect(posted).toEqual(['/api/runs/1/pause', '/api/runs/2/resume', '/api/sources/done/run'])
    expect(native).toEqual([])
  } finally {
    await server.close()
  }
})

test('clearing logs asks in the in-page dialog before posting', async ({ page, request }) => {
  const server = await fixture(page, request)
  try {
    const cleared: string[] = []
    await page.route('**/api/logs/clear', (route) => {
      cleared.push(route.request().method())
      return route.fulfill({ json: { cleared: 2 } })
    })
    const native: string[] = []
    page.on('dialog', async (dialog) => {
      native.push(dialog.message())
      await dialog.dismiss()
    })
    await server.login()
    await page.getByRole('link', { name: '运行记录', exact: true }).click()
    const trigger = page.getByRole('button', { name: '清理日志', exact: true })
    const confirm = page.getByRole('alertdialog', { name: '清理日志' })
    await trigger.click()
    await expect(confirm).toContainText('运行记录、来源尝试与错误摘要会保留')
    await confirm.getByRole('button', { name: '取消', exact: true }).click()
    await expect(confirm).toHaveCount(0)
    expect(cleared).toEqual([])
    await trigger.click()
    await confirm.getByRole('button', { name: '清理', exact: true }).click()
    await expect(page.getByText('已清理 2 份日志')).toBeVisible()
    expect(cleared).toEqual(['POST'])
    expect(native).toEqual([])
  } finally {
    await server.close()
  }
})
