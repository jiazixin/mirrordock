import { test, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'

test('desktop sidebar stays put and only the content column scrolls', async ({ page, request }) => {
  const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-shell-scroll-'))
  const port = await new Promise<number>((resolve, reject) => {
    const listener = net.createServer()
    listener.on('error', reject)
    listener.listen(0, '127.0.0.1', () => {
      const port = (listener.address() as net.AddressInfo).port
      listener.close(() => resolve(port))
    })
  })
  const base = `http://127.0.0.1:${port}`
  const config = path.join(dir, 'config.yaml')
  await writeFile(
    config,
    `listen: 127.0.0.1\nport: ${port}\npath: ./state\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: shell-scroll-fixture-password\nsources: []\n`,
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
    await page.route('**/api/events', (route) =>
      route.fulfill({ contentType: 'text/event-stream', body: '' }),
    )
    await page.setViewportSize({ width: 1440, height: 600 })
    await page.goto(`${base}/admin/analytics/rsync`)
    await page.getByLabel('用户名', { exact: true }).fill('admin')
    await page.getByLabel('管理员密码').fill('shell-scroll-fixture-password')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await expect(page.locator('h1')).toContainText('数据分析')

    const main = page.locator('.main-shell')
    const positions = () =>
      page.evaluate(() => ({
        main: document.querySelector('.main-shell')!.scrollTop,
        window: scrollY,
        sidebar: document.querySelector('.sidebar')!.getBoundingClientRect().top,
      }))
    expect(await main.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true)

    // Wheel input over the sidebar must not reach the content column or the document.
    const sidebar = (await page.locator('.sidebar').boundingBox())!
    await page.mouse.move(sidebar.x + sidebar.width / 2, sidebar.y + sidebar.height / 2)
    await page.mouse.wheel(0, 800)
    await page.waitForTimeout(300)
    expect(await positions()).toEqual({ main: 0, window: 0, sidebar: 0 })

    await page.mouse.move(sidebar.x + sidebar.width + 400, 300)
    await page.mouse.wheel(0, 800)
    await expect.poll(async () => (await positions()).main).toBeGreaterThan(0)
    expect((await positions()).window).toBe(0)
    expect((await positions()).sidebar).toBe(0)

    // A newly entered page starts at the top of the content column.
    await page.getByRole('link', { name: '运行记录' }).click()
    await expect(page.locator('h1')).toContainText('运行记录')
    await expect.poll(async () => (await positions()).main).toBe(0)

    // Narrow screens keep document scrolling with the sticky top bar.
    await page.setViewportSize({ width: 375, height: 600 })
    await page.mouse.move(180, 400)
    await page.mouse.wheel(0, 400)
    await expect.poll(async () => (await positions()).window).toBeGreaterThan(0)
    expect((await positions()).sidebar).toBe(0)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
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
