import { test, expect } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { mkdtemp, writeFile, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'
import http from 'node:http'

async function freePort(): Promise<number> { return new Promise((resolve, reject) => { const server = net.createServer(); server.on('error', reject); server.listen(0, '127.0.0.1', () => { const port = (server.address() as net.AddressInfo).port; server.close(() => resolve(port)) }) }) }
async function stop(child: ChildProcess) { if (child.exitCode !== null) return; child.kill('SIGTERM'); await Promise.race([new Promise(resolve => child.once('exit', resolve)), new Promise(resolve => setTimeout(() => { child.kill('SIGKILL'); resolve(null) }, 5000))]) }

test.use({ timezoneId: 'Asia/Kolkata' })
test('notification settings, saved tests, terminal queue and mobile/help interaction', async ({ page, request }, testInfo) => {
  const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-notifications-'))
  const port = await freePort(); const missingPort = await freePort(); const base = `http://127.0.0.1:${port}`
  const received: { event: string; title: string; body: string }[] = []
  const receiver = http.createServer(async (req, res) => { let body = ''; for await (const chunk of req) body += String(chunk); received.push(JSON.parse(body)); res.writeHead(204).end() })
  await new Promise<void>(resolve => receiver.listen(0, '127.0.0.1', resolve))
  const endpoint = `http://127.0.0.1:${(receiver.address() as net.AddressInfo).port}/fixture-sensitive-key`
  const configFile = path.join(dir, 'config.yaml')
  await writeFile(configFile, `# notification e2e
listen: 127.0.0.1
port: ${port}
path: ./data
mirror_root: ./mirror
auth:
  username: admin
  password: notification-fixture-password
settings:
  task_timeout: 3s
  rsync:
    connect_timeout: 1s
    io_timeout: 1s
sources:
  - id: local
    name: 本地模拟任务
    type: rsync
    enabled: true
    dest: local
    src1: rsync://127.0.0.1:${missingPort}/repo/
`)
  const root = path.resolve(import.meta.dirname, '../..')
  const app = spawn(process.env.MIRRORDOCK_TEST_BINARY ?? path.join(root, 'bin/mirrordock'), ['--config', configFile], { stdio: 'ignore' })
  const errors: string[] = []; page.on('pageerror', error => errors.push(error.message))
  try {
    await expect.poll(async () => { try { return (await request.get(base + '/api/auth/status')).status() } catch { return 0 } }).toBe(200)
    await page.goto(base + '/admin/settings'); await page.getByLabel('用户名', { exact: true }).fill('admin'); await page.getByLabel('管理员密码').fill('notification-fixture-password'); await page.getByRole('button', { name: '登录管理台' }).click()
    await page.getByRole('tab', { name: '通知设置', exact: true }).click()
    await expect(page.locator('.notification-card')).toHaveCount(5)
    const browserZone = await page.evaluate(() => Intl.DateTimeFormat().resolvedOptions().timeZone)
    await expect(page.locator('.timezone-note')).toContainText(browserZone)
    await expect(page.getByText('暂无发送记录。', { exact: false })).toBeVisible()
    const webhook = page.locator('[data-channel="webhook"]')
    await expect(webhook.getByRole('button', { name: '发送测试' })).toBeDisabled()
    await webhook.getByLabel('推送地址').fill(endpoint)
    await expect(webhook.getByLabel('推送地址')).toHaveAttribute('type', 'password')
    await webhook.getByRole('button', { name: '保存设置' }).click(); await expect(webhook.getByRole('status')).toContainText('设置已保存')
    await expect(webhook.getByLabel('推送地址')).toHaveValue(''); await expect(webhook.getByLabel('推送地址')).toHaveAttribute('placeholder', '已保存，留空保持不变')
    let releaseTest!: () => void
    const testGate = new Promise<void>(resolve => { releaseTest = resolve })
    await page.route('**/api/notifications/webhook/test', async route => { await testGate; await route.continue() })
    await webhook.getByRole('button', { name: '发送测试' }).click()
    await expect(webhook.getByRole('button', { name: '保存设置' })).toBeDisabled()
    await expect(webhook.getByRole('button', { name: '发送中…' })).toBeDisabled()
    releaseTest()
    await expect(webhook.getByRole('status')).toContainText('测试通知发送成功'); expect(received[0]?.event).toBe('notification.test')
    await page.unroute('**/api/notifications/webhook/test')
    const yaml = await readFile(configFile, 'utf8'); expect(yaml).toContain('enc:v1:'); expect(yaml).not.toContain('fixture-sensitive-key'); expect(yaml).toContain('# notification e2e')
    // Saving blank preserves the address; enable only after this old task was created.
    const headers = { 'X-MirrorDock-Request': '1' }
    const old = await (await page.request.post(base + '/api/sources/local/run', { headers })).json()
    await expect.poll(async () => (await (await page.request.get(base + `/api/runs/${old.id}`)).json()).state).toMatch(/failed|timed_out/)
    await webhook.getByLabel('启用', { exact: true }).check(); await webhook.getByLabel('仅失败时通知').check(); await webhook.getByRole('button', { name: '保存设置' }).click(); await expect(webhook.getByRole('status')).toContainText('设置已保存')
    const newRun = await (await page.request.post(base + '/api/sources/local/run', { headers })).json()
    await expect.poll(async () => received.filter(item => item.event === 'batch.finished').length, { timeout: 15000 }).toBe(1)
    expect(received.at(-1)?.body).toContain(`#${newRun.id}`); expect(received.at(-1)?.body).toContain(`UTC+05:30 (${browserZone})`)
    await page.getByRole('button', { name: '刷新', exact: true }).click(); await expect(page.locator('.notification-records tbody')).toContainText('已发送'); await expect(page.locator('.notification-records tbody')).not.toContainText(`#${old.id}`)
    const ding = page.locator('[data-channel="dingtalk"]')
    await ding.getByLabel('推送地址').fill('https://oapi.dingtalk.com/robot/send?access_token=fixture-ding-key')
    await ding.getByLabel('加签密钥（可选）', { exact: true }).fill('SEC-fixture-sign'); await ding.getByRole('button', { name: '保存设置' }).click(); await expect(ding.getByRole('status')).toContainText('设置已保存'); await expect(ding.getByLabel('加签密钥（可选）', { exact: true })).toHaveValue('')
    await ding.getByLabel('清除已保存的加签密钥').check(); await ding.getByRole('button', { name: '保存设置' }).click(); await expect(ding.getByRole('status')).toContainText('设置已保存')
    const view = await (await page.request.get(base + '/api/notifications')).json(); expect(view.channels.find((item: { kind: string }) => item.kind === 'dingtalk').has_secret).toBe(false)
    expect(JSON.stringify(view)).not.toContain('fixture-ding-key'); expect(JSON.stringify(view)).not.toContain('SEC-fixture-sign')
    const help = page.getByRole('button', { name: 'Telegram配置帮助' })
    await help.focus(); await expect(page.locator('#help-telegram')).toContainText('群组 ID 保留负号'); await help.press('Escape'); await expect(page.locator('#help-telegram')).toHaveCount(0)
    await help.click(); await expect(page.locator('#help-telegram')).toBeVisible(); await help.click(); await expect(page.locator('#help-telegram')).toHaveCount(0)
    await page.getByRole('button', { name: 'Bark配置帮助' }).hover(); await expect(page.locator('#help-bark')).toBeVisible(); await page.locator('.notification-intro').hover(); await expect(page.locator('#help-bark')).toHaveCount(0)
    for (const width of [1440, 736, 375]) { await page.setViewportSize({ width, height: 1000 }); expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy(); await page.screenshot({ path: testInfo.outputPath(`notifications-${width}.png`), fullPage: true, animations: 'disabled' }) }
    let polls = 0; page.on('request', req => { if (req.url().includes('/api/notifications/records')) polls++ })
    await page.getByRole('tab', { name: '同步执行', exact: true }).click(); const before = polls; await page.waitForTimeout(5200); expect(polls).toBe(before)
    expect(errors).toEqual([])
  } finally { await stop(app); await new Promise<void>(resolve => receiver.close(() => resolve())); await rm(dir, { recursive: true, force: true }) }
})
