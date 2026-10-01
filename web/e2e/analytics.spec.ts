import { test, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'
import type { Analytics, AnalyticsDay, AnalyticsStats } from '../src/app/types'

const stats = (): AnalyticsStats => ({ runs: 0, succeeded: 0, failed: 0, cancelled: 0, active: 0, duration_samples: 0, elapsed_seconds: 0, bytes: 0, files: 0, completed: 0, reused: 0, attempts: 0, failed_attempts: 0, retried_runs: 0, fallback_succeeded: 0 })
function fixture(kind: 'rsync' | 'github_release', count: number, source: string): Analytics {
  const days: AnalyticsDay[] = Array.from({ length: count }, (_, i) => {
    const date = new Date(Date.UTC(2026, 9, 1 - count + i + 1)).toISOString().slice(0, 10)
    const succeeded = (i * 7 + 3) % 12 + 1, failed = i % 5 === 0 ? 2 : 0
    return { ...stats(), date, runs: succeeded + failed, succeeded, failed, duration_samples: succeeded + failed, elapsed_seconds: (succeeded + failed) * (30 + i * 2), bytes: succeeded * 345678901, completed: succeeded * 5, reused: succeeded * 3, files: succeeded * 5, attempts: succeeded + failed + 3, failed_attempts: failed + 3, fallback_succeeded: 1, retried_runs: failed ? 1 : 0 }
  })
  const summary = stats()
  for (const day of days) for (const key of Object.keys(summary) as (keyof AnalyticsStats)[]) summary[key] += day[key]
  return { type: kind, from: `${days[0]!.date}T00:00:00+08:00`, until: '2026-10-01T12:00:00+08:00', timezone: 'Asia/Shanghai', summary, days, sources: [{ ...summary, id: source || 'fixture', name: source === 'slow' ? '较慢的上游' : '镜像测试源' }], errors: [{ code: kind === 'rsync' ? 'SOURCE_EXHAUSTED' : 'GITHUB_RATE_LIMIT', count: summary.failed }] }
}

test('analytics charts, filtering, request races, empty/error and mobile states', async ({ page, request }, testInfo) => {
  const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-analytics-'))
  const port = await new Promise<number>((resolve, reject) => { const server = net.createServer(); server.on('error', reject); server.listen(0, '127.0.0.1', () => { const n = (server.address() as net.AddressInfo).port; server.close(() => resolve(n)) }) })
  const base = `http://127.0.0.1:${port}`, config = path.join(dir, 'config.yaml')
  await writeFile(config, `listen: 127.0.0.1\nport: ${port}\npath: ./state\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: analytics-fixture-password\nsources: []\n`)
  const app = spawn(process.env.MIRRORDOCK_TEST_BINARY ?? path.resolve(import.meta.dirname, '../../bin/mirrordock'), ['-f', config], { stdio: 'ignore' })
  const errors: string[] = []; page.on('pageerror', e => errors.push(e.message))
  try {
    await expect.poll(async () => { try { return (await request.get(`${base}/api/auth/status`)).status() } catch { return 0 } }).toBe(200)
    await page.route('**/api/events', route => route.fulfill({ contentType: 'text/event-stream', body: '' }))
    await page.goto(`${base}/admin/analytics/rsync`)
    await page.getByLabel('用户名', { exact: true }).fill('admin'); await page.getByLabel('管理员密码').fill('analytics-fixture-password')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await page.getByRole('link', { name: 'Rsync 分析', exact: true }).click()
    await expect(page.getByText('这段时间暂无运行记录', { exact: true })).toBeVisible()
    await expect(page.locator('.result-ring strong')).toHaveText('—')
    let fail = false, delayed = false
    await page.route('**/api/config', async route => { const res = await route.fetch(), json = await res.json(); json.config.sources = [{ id: 'fixture', name: '镜像测试源', type: 'rsync' }, { id: 'slow', name: '较慢的上游', type: 'rsync' }]; await route.fulfill({ json }) })
    await page.route('**/api/analytics?*', async route => {
      const q = new URL(route.request().url()).searchParams
      if (delayed && q.get('source') === 'slow') await new Promise(resolve => setTimeout(resolve, 350))
      await route.fulfill({ status: fail ? 503 : 200, json: fail ? { error: '分析暂时不可用，请稍后重试' } : fixture(q.get('type') as Analytics['type'], Number(q.get('days')), q.get('source') || '') })
    })
    // Restore session to load the synthetic source options through the same API.
    await page.reload()
    await expect(page.locator('.analytics-stats article').first()).toContainText(String(fixture('rsync', 30, '').summary.runs))
    await expect(page.locator('.insight-panel')).toContainText('备用源成功任务')
    await expect(page.locator('.error-bars')).toContainText('SOURCE_EXHAUSTED')
    await expect(page.locator('.chart-hit')).toHaveCount(30)
    await page.locator('.chart-hit').last().focus(); await expect(page.locator('.trend-readout')).toContainText('2026-10-01')
    await page.getByRole('button', { name: '处理量', exact: true }).click(); await expect(page.locator('.trend-panel path')).toHaveCount(2)
    await page.getByRole('button', { name: '耗时', exact: true }).click(); await expect(page.locator('.trend-panel')).toContainText('无样本日期以 0 绘制')
    await page.getByRole('button', { name: '次数', exact: true }).click()
    await page.screenshot({ path: testInfo.outputPath('rsync-desktop.png'), fullPage: true, animations: 'disabled' })
    const chooseDays = async (label: string) => { await page.getByRole('combobox', { name: '分析时间范围' }).click(); await page.getByRole('option', { name: label, exact: true }).click() }
    await chooseDays('近 7 天'); await expect(page.locator('.chart-hit')).toHaveCount(7)
    await chooseDays('近 90 天'); await expect(page.locator('.chart-hit')).toHaveCount(90)
    delayed = true
    const source = page.getByRole('combobox', { name: '分析同步源' })
    await source.click(); await page.getByRole('option', { name: '较慢的上游', exact: true }).click()
    await source.click(); await page.getByRole('option', { name: '镜像测试源', exact: true }).click()
    await expect(page.locator('.source-ranking tbody')).toContainText('镜像测试源')
    await page.waitForTimeout(450); await expect(page.locator('.source-ranking tbody')).not.toContainText('较慢的上游')
    await page.getByRole('link', { name: 'GitHub Release 分析', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'GitHub Release 分析.' })).toBeVisible()
    await expect(source).toContainText('全部同步源')
    const gh = fixture('github_release', 90, '').summary
    await expect(page.locator('.asset-labels')).toContainText(String(gh.reused))
    await expect(page.locator('.insight-total strong')).toHaveText(gh.completed.toLocaleString())
    await expect(page.locator('.error-bars')).toContainText('GITHUB_RATE_LIMIT')
    await page.screenshot({ path: testInfo.outputPath('github-desktop.png'), fullPage: true, animations: 'disabled' })
    for (const width of [1024, 736, 375, 320]) {
      await page.setViewportSize({ width, height: 900 })
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await expect(page.locator('.asset-labels')).toBeVisible()
      await page.screenshot({ path: testInfo.outputPath(`github-${width}.png`), fullPage: true, animations: 'disabled' })
    }
    fail = true; await page.getByRole('button', { name: '刷新', exact: true }).click()
    await expect(page.getByRole('alert')).toContainText('分析暂时不可用')
    fail = false; await page.getByRole('button', { name: '重试', exact: true }).click(); await expect(page.locator('.insight-panel')).toBeVisible()
    expect(errors).toEqual([])
  } finally {
    if (app.exitCode === null) { app.kill('SIGTERM'); await Promise.race([new Promise(resolve => app.once('exit', resolve)), new Promise(resolve => setTimeout(() => { app.kill('SIGKILL'); resolve(null) }, 5000))]) }
    await rm(dir, { recursive: true, force: true })
  }
})
