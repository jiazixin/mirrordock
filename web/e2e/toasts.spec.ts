import { test, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'

test('floating feedback shrinks for five seconds, repeats independently and fits mobile', async ({
  page,
  request,
}, testInfo) => {
  const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-toasts-'))
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
    `listen: 127.0.0.1\nport: ${port}\npath: ./state\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: toast-fixture-password\nsources: []\n`,
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
    await page.goto(`${base}/admin/settings`)
    await page.getByLabel('用户名', { exact: true }).fill('admin')
    await page.getByLabel('管理员密码').fill('toast-fixture-password')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await expect(page.getByRole('heading', { name: '系统设置' })).toBeVisible()
    await page.getByRole('tab', { name: 'GitHub Release', exact: true }).click()
    const heading = page.locator('.page-heading')
    const before = await heading.boundingBox()
    const save = page.getByRole('button', { name: '保存设置', exact: true })
    const cards = page.locator('.toast-card')
    await save.click()
    await expect(cards).toHaveCount(1)
    await expect(cards.getByRole('status')).toHaveText('配置已写回 YAML')
    await expect(cards).toHaveCSS('background-color', 'rgb(255, 255, 255)')
    expect((await heading.boundingBox())?.y).toBe(before?.y)
    const first = cards.first()
    const fraction = () =>
      first
        .locator('.toast-countdown')
        .evaluate(
          (el) =>
            el.getBoundingClientRect().width / el.parentElement!.getBoundingClientRect().width,
        )
    const start = await fraction()
    await page.waitForTimeout(1200)
    expect(await fraction()).toBeLessThan(start - 0.15)
    await page.screenshot({ path: testInfo.outputPath('toast-desktop.png') })
    await save.click()
    await expect(cards).toHaveCount(2)
    await expect(cards).toHaveCount(1, { timeout: 4500 })
    // The first copy's timeout must leave the newer, identical message visible.
    await expect(cards.getByRole('status')).toHaveText('配置已写回 YAML')
    await expect(cards).toHaveCount(0, { timeout: 2500 })
    for (let i = 0; i < 4; i++) {
      await save.click()
      await expect(cards).toHaveCount(Math.min(i + 1, 3))
    }
    await cards.last().getByRole('button', { name: '关闭提示' }).click()
    await expect(cards).toHaveCount(2)
    await cards.first().getByRole('button', { name: '关闭提示' }).focus()
    await page.keyboard.press('Enter')
    await expect(cards).toHaveCount(1)
    await cards.getByRole('button', { name: '关闭提示' }).click()
    await expect(cards).toHaveCount(0)
    await page.route('**/api/config/reload', (route) =>
      route.fulfill({ status: 409, json: { error: '<b>配置冲突</b>，请重试' } }),
    )
    await page.getByRole('button', { name: '重新载入配置' }).click()
    await expect(cards.getByRole('alert')).toContainText('<b>配置冲突</b>')
    await expect(cards.locator('b')).toHaveCount(0)
    await expect(cards).toHaveClass(/error/)
    await cards.getByRole('button', { name: '关闭提示' }).click()
    await expect(cards).toHaveCount(0)
    for (const width of [375, 320]) {
      await page.setViewportSize({ width, height: 900 })
      await save.click()
      await expect(cards).toHaveCount(1)
      await page.waitForTimeout(250)
      const box = (await cards.boundingBox())!
      expect(box.x).toBeGreaterThanOrEqual(0)
      expect(box.x + box.width).toBeLessThanOrEqual(width)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      )
      await page.screenshot({ path: testInfo.outputPath(`toast-${width}.png`) })
      await cards.getByRole('button', { name: '关闭提示' }).click()
      await expect(cards).toHaveCount(0)
    }
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await save.click()
    await expect(cards).toHaveCount(1)
    await expect(cards.locator('.toast-countdown')).toHaveCSS('animation-duration', '5s')
    await page.getByRole('button', { name: '打开导航' }).click()
    await page.getByRole('button', { name: '退出登录' }).click()
    await expect(cards).toHaveCount(0)
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
