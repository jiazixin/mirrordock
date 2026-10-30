import { test, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { appendFile, mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'

test('configuration errors from the server are shown in Chinese by code', async ({
  page,
  request,
}) => {
  const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-config-errors-'))
  const port = await new Promise<number>((resolve, reject) => {
    const listener = net.createServer()
    listener.on('error', reject)
    listener.listen(0, '127.0.0.1', () => {
      const port = (listener.address() as net.AddressInfo).port
      listener.close(() => {
        resolve(port)
      })
    })
  })
  const base = `http://127.0.0.1:${port}`
  const config = path.join(dir, 'config.yaml')
  await writeFile(
    config,
    `listen: 127.0.0.1\nport: ${port}\npath: ./state\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: config-error-fixture-password\nsources: []\n`,
  )
  const app = spawn(
    process.env.MIRRORDOCK_TEST_BINARY ?? path.resolve(import.meta.dirname, '../../bin/mirrordock'),
    ['-c', config],
    { stdio: 'ignore' },
  )
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
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
    await page.route('**/api/events', (route) =>
      route.fulfill({ contentType: 'text/event-stream', body: '' }),
    )
    await page.goto(`${base}/admin/settings`)
    await page.getByLabel('用户名', { exact: true }).fill('admin')
    await page.getByLabel('管理员密码').fill('config-error-fixture-password')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await expect(page.getByRole('heading', { name: '系统设置' })).toBeVisible()
    const cards = page.locator('.toast-card')
    const save = page.getByRole('button', { name: '保存设置', exact: true })

    // A field error maps its reason code to Chinese and keeps the field path.
    const put = page.waitForResponse(
      (response) => response.url().endsWith('/api/config') && response.request().method() === 'PUT',
    )
    await page.getByLabel('Rsync I/O 超时').fill('0s')
    await save.click()
    const alert = cards.getByRole('alert')
    await expect(alert).toHaveText('settings.rsync.io_timeout：必须为正时间长度，例如 30s 或 8h')
    expect(await (await put).json()).toEqual({
      error: {
        code: 'invalid_argument',
        message: 'settings.rsync.io_timeout: must be a positive duration such as 30s or 8h',
        details: { fields: [{ path: 'settings.rsync.io_timeout', reason: 'invalid_duration' }] },
      },
    })
    await cards.getByRole('button', { name: '关闭提示' }).click()
    await expect(cards).toHaveCount(0)

    // A file changed on disk since loading is a conflict with its own text.
    await page.getByLabel('Rsync I/O 超时').fill('5m')
    await appendFile(config, '# edited outside the console\n')
    await save.click()
    await expect(alert).toHaveText('配置已被修改，请重新加载后再保存')
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
