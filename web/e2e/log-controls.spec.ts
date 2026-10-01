import { test, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'

test('log refresh releases its state and follow defaults on, toggles and survives attempt changes', async ({ page, request }, testInfo) => {
  const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-log-controls-'))
  const port = await new Promise<number>((resolve, reject) => {
    const server = net.createServer(); server.on('error', reject)
    server.listen(0, '127.0.0.1', () => { const value = (server.address() as net.AddressInfo).port; server.close(() => resolve(value)) })
  })
  const base = `http://127.0.0.1:${port}`
  const config = path.join(dir, 'config.yaml')
  await writeFile(config, `listen: 127.0.0.1\nport: ${port}\npath: ./state\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: log-controls-fixture-password\nsources: []\n`)
  const app = spawn(process.env.MIRRORDOCK_TEST_BINARY ?? path.resolve(import.meta.dirname, '../../bin/mirrordock'), ['-f', config], { stdio: 'ignore' })
  const errors: string[] = []; page.on('pageerror', error => errors.push(error.message))
  let pending: (() => void) | undefined
  try {
    await expect.poll(async () => { try { return (await request.get(`${base}/api/auth/status`)).status() } catch { return 0 } }).toBe(200)
    const run = { id: 1, source_id: 'fixture', name: '日志控件测试', type: 'rsync', state: 'succeeded', trigger: 'manual', created_at: new Date().toISOString(), elapsed_seconds: 1, budget_seconds: 30, progress: { phase: 'finished', bytes: 0, success: 1, failed: 0, skipped: 0 }, attempts: [1, 2].map(number => ({ number, candidate: `src${number}`, started_at: new Date().toISOString() })), events: [], snapshot: {} }
    await page.route('**/api/runs?*', route => route.fulfill({ json: { items: [run], total: 1 } }))
    await page.route('**/api/runs/1', route => route.fulfill({ json: run }))
    await page.route('**/api/events', route => route.fulfill({ contentType: 'text/event-stream', body: '' }))
    let suffix = '初始末行', failure = false
    await page.route('**/api/runs/1/logs?*', async route => {
      await new Promise<void>(resolve => { pending = resolve })
      pending = undefined
      if (failure) await route.fulfill({ status: 500, json: { error: '日志读取测试失败' } })
      else {
        const offset = Number(new URL(route.request().url()).searchParams.get('offset'))
        const text = offset === 0 ? '初始行\n'.repeat(120) + suffix : `\n${suffix}`
        await route.fulfill({ json: { text, next_offset: offset + Buffer.byteLength(text) } })
      }
    })
    await page.goto(`${base}/admin/`)
    await page.getByLabel('用户名', { exact: true }).fill('admin'); await page.getByLabel('管理员密码').fill('log-controls-fixture-password')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await page.getByRole('button', { name: '新增同步源' }).first().click()
    expect(await page.locator('.form-section h3').allTextContents()).toEqual(['运行计划', '同步来源按数字顺序尝试，切换不重置任务预算', 'rsync 选项'])
    await page.getByRole('button', { name: '取消', exact: true }).click()
    await page.getByRole('link', { name: '运行记录', exact: true }).click()
    await page.getByRole('button', { name: '查看记录详情' }).first().click()
    const refresh = page.getByRole('button', { name: '刷新日志', exact: true })
    const follow = page.getByRole('button', { name: '持续跟随最新日志', exact: true })
    const log = page.locator('.log-view')
    const release = async () => { await expect.poll(() => !!pending).toBe(true); pending!() }
    const atBottom = () => log.evaluate(el => Math.abs(el.scrollHeight - el.clientHeight - el.scrollTop) < 2)
    await expect(follow).toHaveAttribute('aria-pressed', 'true')
    await release(); await expect(log).toContainText('初始末行'); await expect.poll(atBottom).toBe(true)
    await refresh.hover()
    const idleColor = await refresh.evaluate(el => getComputedStyle(el).color)
    const refreshLog = async (last: string) => {
      suffix = last
      // A timer may already have started a request; both paths must release the same state.
      if (!pending) await refresh.click()
      await expect(refresh).toHaveAttribute('aria-busy', 'true'); await expect(refresh).toBeDisabled()
      await release(); await expect(refresh).toHaveAttribute('aria-busy', 'false'); await expect(refresh).toBeEnabled()
    }
    await refreshLog('新追加末行'); await expect(log).toContainText('新追加末行'); await expect.poll(atBottom).toBe(true)
    await refresh.hover(); expect(await refresh.evaluate(el => getComputedStyle(el).color)).toBe(idleColor)
    await follow.click(); await expect(follow).toHaveAttribute('aria-pressed', 'false')
    await log.evaluate(el => { el.scrollTop = 0 })
    await refreshLog('暂停跟随后追加'); await expect(log).toContainText('暂停跟随后追加'); expect(await log.evaluate(el => el.scrollTop)).toBe(0)
    await follow.click(); await expect.poll(atBottom).toBe(true)
    failure = true; await refreshLog('失败'); await expect(page.getByRole('alert')).toContainText('日志读取测试失败')
    failure = false; await refreshLog('恢复成功'); await expect(page.getByRole('alert')).toHaveCount(0)
    await page.getByRole('combobox', { name: '选择日志尝试' }).click(); await page.getByRole('option', { name: '尝试 #1', exact: true }).click()
    await expect(refresh).toBeDisabled(); await release(); await expect(log).toContainText('恢复成功'); await expect.poll(atBottom).toBe(true)
    await page.screenshot({ path: testInfo.outputPath('log-follow-desktop.png'), fullPage: true })
    for (const width of [375, 320]) {
      await page.setViewportSize({ width, height: 900 })
      expect(await page.getByRole('dialog').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
      await expect(follow).toBeVisible()
      await page.screenshot({ path: testInfo.outputPath(`log-follow-${width}.png`), fullPage: true })
    }
    await page.getByRole('button', { name: '关闭', exact: true }).click()
    await page.getByRole('button', { name: '查看记录详情' }).first().click()
    await expect(follow).toHaveAttribute('aria-pressed', 'true'); await release(); await expect.poll(atBottom).toBe(true)
    await page.getByRole('button', { name: '关闭', exact: true }).click()
    expect(errors).toEqual([])
  } finally {
    pending?.()
    if (app.exitCode === null) { app.kill('SIGTERM'); await Promise.race([new Promise(resolve => app.once('exit', resolve)), new Promise(resolve => setTimeout(() => { app.kill('SIGKILL'); resolve(null) }, 5000))]) }
    await rm(dir, { recursive: true, force: true })
  }
})
