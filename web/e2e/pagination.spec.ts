import { test, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'

test('management pagination covers complete history, filtering, page sizes, jump and mobile layout', async ({
  page,
  request,
}, testInfo) => {
  const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-pagination-'))
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
    `listen: 127.0.0.1\nport: ${port}\npath: ./state\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: pagination-fixture-password\nsources: []\n`,
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
    const sources = Array.from({ length: 125 }, (_, index) => ({
      id: `fixture-${index + 1}`,
      name: `测试源 ${index + 1}`,
      type: 'rsync',
      dest: `fixture-${index + 1}`,
      enabled: false,
      upstreams: { src1: 'rsync://127.0.0.1/fixture/', src2: 'rsync://127.0.0.1/backup/' },
      schedule: { enabled: false, cron: '0 0 * * *' },
    }))
    const runs = Array.from({ length: 125 }, (_, index) => ({
      id: 125 - index,
      source_id: sources[index % 125]!.id,
      name: `任务 ${125 - index}`,
      type: 'rsync',
      state: 'succeeded',
      trigger: index % 2 ? 'manual' : 'schedule',
      created_at: new Date(Date.now() - index * 1000).toISOString(),
      elapsed_seconds: 1,
      progress: {
        phase: 'finished',
        bytes: 1048576,
        received_bytes: index === 0 ? null : 1024,
        received_incomplete: index === 1,
        percent: 100,
        success: 1,
        failed: 0,
        skipped: 0,
      },
      attempts: [],
      events: [],
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
    let delayed = false
    await page.route('**/api/runs?*', async (route) => {
      const q = new URL(route.request().url()).searchParams
      const matching = runs.filter(
        (run) =>
          (!q.get('state') || run.state === q.get('state')) &&
          (!q.get('source') || run.source_id === q.get('source')) &&
          (!q.get('trigger') || run.trigger === q.get('trigger')),
      )
      const offset = Number(q.get('offset') ?? 0),
        limit = Number(q.get('limit') ?? 50)
      if (delayed && q.get('source') === 'fixture-1')
        await new Promise((resolve) => setTimeout(resolve, 400))
      await route.fulfill({
        json: { items: matching.slice(offset, offset + limit), total: matching.length },
      })
    })
    const deliveries = runs.map((run) => ({
      id: run.id,
      run_id: run.id,
      channel: 'webhook',
      state: 'sent',
      attempts: 1,
      updated_at: run.created_at,
      error: '',
    }))
    await page.route('**/api/notifications/records?*', (route) => {
      const q = new URL(route.request().url()).searchParams,
        offset = Number(q.get('offset')),
        limit = Number(q.get('limit'))
      return route.fulfill({
        json: { items: deliveries.slice(offset, offset + limit), total: deliveries.length },
      })
    })
    await page.goto(`${base}/admin/`)
    await page.getByLabel('用户名', { exact: true }).fill('admin')
    await page.getByLabel('管理员密码').fill('pagination-fixture-password')
    await page.getByRole('button', { name: '登录管理台' }).click()
    const pager = page.getByRole('navigation', { name: '列表分页' })
    const chooseSize = async (size: number) => {
      await pager.getByRole('combobox', { name: '每页条数' }).click()
      await page.getByRole('option', { name: `${size} 条 / 页`, exact: true }).click()
    }
    const jump = async (number: string) => {
      await pager.getByLabel('跳转页码').fill(number)
      await pager.getByRole('button', { name: '跳转', exact: true }).click()
    }
    await expect(page.locator('.source-table tbody tr')).toHaveCount(50)
    const locationRow = page.locator('.source-table tbody tr').first()
    const origin = await locationRow.locator('.source-origin').boundingBox()
    const dest = await locationRow.locator('.dest').boundingBox()
    expect(Math.abs(origin!.y + origin!.height / 2 - dest!.y - dest!.height / 2)).toBeLessThan(2)
    const rsyncIcon = await page
      .getByRole('link', { name: 'Rsync 分析', exact: true })
      .locator('svg')
      .innerHTML()
    const repoIcon = await page
      .getByRole('link', { name: 'Reposync分析', exact: true })
      .locator('svg')
      .innerHTML()
    expect(rsyncIcon).not.toEqual(repoIcon)
    await page.screenshot({ path: testInfo.outputPath('source-alignment.png'), fullPage: false })
    await expect(pager).toContainText('共 125 个同步源')
    await pager.getByRole('button', { name: '末页', exact: true }).click()
    await expect(page.locator('.source-table tbody tr')).toHaveCount(25)
    await expect(pager.getByRole('button', { name: '下一页', exact: true })).toBeDisabled()
    await chooseSize(20)
    await expect(page.locator('.source-table tbody tr')).toHaveCount(20)
    await expect(pager).toContainText('第 1 / 7 页')
    await jump('6')
    await expect(page.locator('.source-table tbody')).toContainText('测试源 101')
    await page.getByLabel('搜索同步源').fill('fixture-125')
    await expect(page.locator('.source-table tbody tr')).toHaveCount(1)
    await expect(pager).toContainText('第 1 / 1 页')
    await page.getByLabel('搜索同步源').fill('no-match')
    await expect(pager).toContainText('共 0 个同步源')
    await expect(pager.getByRole('button', { name: '末页', exact: true })).toBeDisabled()
    await page.getByRole('link', { name: '计划管理', exact: true }).click()
    await expect(page.locator('tbody tr')).toHaveCount(50)
    await jump('3')
    await expect(page.locator('tbody tr')).toHaveCount(25)
    await page.getByRole('link', { name: '批次记录', exact: true }).click()
    await expect(page.locator('.run-row')).toHaveCount(50)
    await expect(page.locator('.run-row').first()).toContainText('实际接收 未统计')
    await expect(page.locator('.run-row').nth(1)).toContainText('实际接收 1.0 KiB（部分统计）')
    await expect(page.locator('.run-row').first().locator('.run-subtitle')).not.toContainText(
      'fixture-',
    )
    const rangeFilter = page.getByRole('combobox', { name: '时间范围', exact: true })
    for (const [key, label] of [
      ['1h', '最近 1 小时'],
      ['12h', '最近 12 小时'],
      ['24h', '最近 24 小时'],
      ['yesterday', '昨天'],
      ['3d', '最近 3 天'],
      ['7d', '最近 7 天'],
      ['month', '本月'],
    ]) {
      await rangeFilter.click()
      const requested = page.waitForRequest(
        (req) =>
          req.url().includes('/api/runs?') && new URL(req.url()).searchParams.get('range') === key,
      )
      await page.getByRole('option', { name: label, exact: true }).click()
      await requested
      await expect(pager).toContainText('第 1 / 3 页')
    }
    await rangeFilter.click()
    await page.getByRole('option', { name: '全部时间', exact: true }).click()
    await expect(pager).toContainText('共 125 个批次')
    await jump('3')
    await expect(page.locator('.run-row')).toHaveCount(25)
    await expect(page.locator('.run-row').last()).toContainText('#1')
    await pager.getByLabel('跳转页码').fill('4')
    await expect(pager.getByRole('button', { name: '跳转', exact: true })).toBeDisabled()
    await pager.getByLabel('跳转页码').fill('1.5')
    await expect(pager.getByRole('button', { name: '跳转', exact: true })).toBeDisabled()
    await page.getByRole('combobox', { name: '运行来源', exact: true }).click()
    await page.getByRole('option', { name: '手动触发', exact: true }).click()
    await expect(pager).toContainText('共 62 个批次')
    await expect(pager).toContainText('第 1 / 2 页')
    await expect(page.locator('.run-row')).toHaveCount(50)
    await chooseSize(100)
    await expect(page.locator('.run-row')).toHaveCount(62)
    await page.screenshot({
      path: testInfo.outputPath('batch-pagination-desktop.png'),
      fullPage: true,
    })
    await page
      .locator('.filters-grid')
      .screenshot({ path: testInfo.outputPath('batch-filters.png') })
    await pager.screenshot({ path: testInfo.outputPath('batch-footer.png') })
    expect(
      await page
        .getByRole('combobox', { name: '时间范围', exact: true })
        .evaluate((el) => el.getBoundingClientRect().width),
    ).toBeLessThanOrEqual(180)
    for (const width of [736, 375, 320]) {
      await page.setViewportSize({ width, height: 900 })
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      )
      await expect(pager.getByLabel('跳转页码')).toBeVisible()
      await page.screenshot({
        path: testInfo.outputPath(`batch-pagination-${width}.png`),
        fullPage: true,
      })
      await pager.screenshot({ path: testInfo.outputPath(`batch-footer-${width}.png`) })
      if (width === 320)
        await page.screenshot({ path: testInfo.outputPath('batch-mobile-viewport.png') })
    }
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.getByRole('link', { name: '运行记录', exact: true }).click()
    await expect(page.locator('tbody tr')).toHaveCount(50)
    await expect(page.getByRole('columnheader', { name: '耗时', exact: true })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: '数据量', exact: true })).toBeVisible()
    await expect(page.locator('tbody tr').first().locator('td').nth(4)).toHaveText('1.0 MiB')
    await page.screenshot({ path: testInfo.outputPath('records-columns.png'), fullPage: false })
    await chooseSize(20)
    await jump('7')
    await expect(page.locator('tbody tr')).toHaveCount(5)
    delayed = true
    const sourceFilter = page.getByRole('combobox', { name: '按同步源筛选', exact: true })
    await sourceFilter.click()
    await page.getByRole('option', { name: '测试源 1', exact: true }).click()
    await sourceFilter.click()
    await page.getByRole('option', { name: '测试源 2', exact: true }).click()
    await expect(pager).toContainText('共 1 条记录')
    await expect(page.locator('tbody')).toContainText('任务 124')
    await page.waitForTimeout(500)
    await expect(page.locator('tbody')).toContainText('任务 124')
    runs.splice(0)
    await page.getByRole('button', { name: '刷新', exact: true }).click()
    await expect(pager).toContainText('共 0 条记录')
    await page.getByRole('button', { name: '失败记录', exact: true }).click()
    await expect(pager).toContainText('共 0 条记录')
    await page.getByRole('link', { name: '系统设置', exact: true }).click()
    await page.getByRole('tab', { name: '系统配置', exact: true }).click()
    await expect(page.locator('.check-row')).toHaveCount(9)
    await expect(page.locator('.check-row').filter({ hasText: 'RPM 公钥工具' })).toBeVisible()
    for (const status of await page.locator('.check-status').all()) {
      const centered = await status.evaluate((el) => {
        const style = getComputedStyle(el.parentElement!)
        const widths = style.gridTemplateColumns.split(' ').map(parseFloat)
        const row = el.parentElement!.getBoundingClientRect()
        const box = el.getBoundingClientRect()
        return (
          Math.abs(
            box.x +
              box.width / 2 -
              row.x -
              widths[0]! -
              parseFloat(style.columnGap) -
              widths[1]! / 2,
          ) < 2
        )
      })
      expect(centered).toBe(true)
    }
    await page.screenshot({
      path: testInfo.outputPath('environment-centering.png'),
      fullPage: true,
    })
    await page.getByRole('tab', { name: '通知设置', exact: true }).click()
    await expect(page.locator('.notification-records tbody tr')).toHaveCount(50)
    await jump('3')
    await expect(page.locator('.notification-records tbody tr')).toHaveCount(25)
    await chooseSize(100)
    await expect(page.locator('.notification-records tbody tr')).toHaveCount(100)
    await expect(pager).toContainText('第 1 / 2 页')
    await jump('2')
    deliveries.splice(5)
    await page
      .locator('.notification-records')
      .getByRole('button', { name: '刷新', exact: true })
      .click()
    await expect(pager).toContainText('第 1 / 1 页')
    await expect(page.locator('.notification-records tbody tr')).toHaveCount(5)
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
