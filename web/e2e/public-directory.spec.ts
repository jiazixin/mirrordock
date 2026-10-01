import { test, expect } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { mkdtemp, writeFile, rm, mkdir } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'

async function freePort(): Promise<number> {
 return new Promise((resolve, reject) => {
  const server = net.createServer()
  server.on('error', reject)
  server.listen(0, '127.0.0.1', () => {
   const port = (server.address() as net.AddressInfo).port
   server.close(() => resolve(port))
  })
 })
}

async function stop(child: ChildProcess) {
 if (child.exitCode !== null || child.signalCode !== null) return
 await new Promise<void>(resolve => {
  const timer = setTimeout(() => { child.kill('SIGKILL'); resolve() }, 5000)
  child.once('exit', () => { clearTimeout(timer); resolve() })
  child.kill('SIGTERM')
 })
}

test('public directory layout, icons, filtering, sorting, pagination and safe file links', async ({ page, request, browser }, testInfo) => {
 const rsync = process.env.MIRRORDOCK_TEST_RSYNC
 test.skip(!rsync, 'Set MIRRORDOCK_TEST_RSYNC to a modern rsync executable')
 const root = path.resolve(import.meta.dirname, '../..')
 const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-directory-'))
 const port = await freePort(), daemonPort = await freePort()
 const base = `http://127.0.0.1:${port}`
 const upstream = path.join(dir, 'upstream')
 await mkdir(path.join(upstream, 'packages'), { recursive: true })
 await mkdir(path.join(upstream, 'releases'))
 await mkdir(path.join(upstream, 'docs'))
 const files = ['mirror-client-2.4.0.x86_64.rpm', 'mirror-client_2.4.0_amd64.deb', 'source-2.4.0.tar.gz', 'source-2.4.0.tar.xz', 'assets.zip', 'manifest.json', 'repository.yaml', 'SHA256SUMS', 'SHA256SUMS.asc', 'RPM-GPG-KEY-mirror', 'installer.iso', 'README.md']
 for (const [i, name] of files.entries()) await writeFile(path.join(upstream, name), Buffer.alloc((i + 1) * 1024, 65))
 const hostile = '<img src=x onerror=alert(1)>.json'
 await writeFile(path.join(upstream, 'docs', hostile), 'escaped filename')
 for (let i = 1; i <= 55; i++) await writeFile(path.join(upstream, 'packages', `package-${String(i).padStart(3, '0')}.rpm`), Buffer.alloc(i * 128, 66))
 await writeFile(path.join(dir, 'rsyncd.conf'), `use chroot = no\nuid =\ngid =\nmunge symlinks = no\n[repo]\npath = ${upstream}\nread only = yes\n`)
 const daemon = spawn(rsync!, ['--daemon', '--no-detach', '--address=127.0.0.1', `--port=${daemonPort}`, `--config=${path.join(dir, 'rsyncd.conf')}`], { stdio: 'ignore' })
 const configFile = path.join(dir, 'config.yaml')
 await writeFile(configFile, `listen: 127.0.0.1\nport: ${port}\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: mirror-admin\n  password: isolated directory password 123\nsources:\n  - id: isolated\n    name: 文件列表测试\n    type: rsync\n    enabled: true\n    src1: rsync://127.0.0.1:${daemonPort}/repo/\n    dest: github/mirror-client\n`)
 const app = spawn(process.env.MIRRORDOCK_TEST_BINARY ?? path.join(root, 'bin', 'mirrordock'), ['--config', configFile], { env: { ...process.env, TZ: 'Asia/Shanghai', PATH: `${path.dirname(rsync!)}${path.delimiter}${process.env.PATH}` }, stdio: 'ignore' })
 const errors: string[] = []
 page.on('pageerror', error => errors.push(error.message))
 try {
  await expect.poll(async () => { try { return (await request.get(base + '/api/auth/status')).status() } catch { return 0 } }).toBe(200)
  await page.goto(base + '/admin/')
  await page.getByLabel('用户名', { exact: true }).fill('mirror-admin')
  await page.getByLabel('管理员密码').fill('isolated directory password 123')
  await page.getByRole('button', { name: '登录管理台' }).click()
  await page.getByRole('row').filter({ hasText: '文件列表测试' }).getByRole('button', { name: '同步', exact: true }).click()
  await expect.poll(async () => (await request.get(base + '/github/mirror-client/manifest.json')).status(), { timeout: 20000 }).toBe(200)
  // A fresh browser context proves that public browsing does not require login.
  const publicContext = await browser.newContext({ viewport: { width: 1440, height: 1250 } })
  try {
   const publicPage = await publicContext.newPage()
   publicPage.on('pageerror', error => errors.push(error.message))
   publicPage.on('dialog', async dialog => { errors.push(`unexpected dialog: ${dialog.message()}`); await dialog.dismiss() })
   await publicPage.goto(base + '/')
   await publicPage.getByRole('link', { name: 'github', exact: true }).click()
   await publicPage.getByRole('link', { name: 'mirror-client', exact: true }).click()
   const rows = publicPage.locator('#file-rows tr:visible')
   await expect(rows).toHaveCount(15)
   await expect(rows.first()).toContainText('docs')
   await expect(publicPage.locator('.file-icon.folder')).toHaveCount(3)
   await expect(publicPage.locator('.file-icon.package')).toHaveCount(2)
   await expect(publicPage.locator('.file-icon.archive')).toHaveCount(3)
   await expect(rows.filter({ hasText: 'manifest.json' })).toContainText('6.0 KiB')
   await expect(publicPage.locator('time').first()).toHaveAttribute('datetime', /\+08:00$/)
   await publicPage.screenshot({ path: testInfo.outputPath('public-directory-desktop.png'), fullPage: true })
   await publicPage.getByRole('button', { name: '软件包', exact: true }).click()
   await expect(rows).toHaveCount(2)
   await publicPage.getByRole('button', { name: '压缩包', exact: true }).click()
   await expect(rows).toHaveCount(3)
   const search = publicPage.getByRole('searchbox', { name: '搜索当前目录' })
   await search.fill('does-not-exist')
   await expect(publicPage.getByRole('heading', { name: '没有匹配的文件' })).toBeVisible()
   await publicPage.getByRole('button', { name: '清除筛选' }).click()
   await expect(rows).toHaveCount(15)
   await publicPage.getByRole('link', { name: 'docs', exact: true }).click()
   await search.fill('<img')
   await expect(rows).toHaveCount(1)
   await expect(rows.first()).toContainText(hostile)
   expect(await publicPage.locator('img').count()).toBe(0)
   const download = await publicContext.request.get(base + '/github/mirror-client/docs/' + encodeURIComponent(hostile))
   expect(download.status()).toBe(200)
   expect(await download.text()).toBe('escaped filename')
   await publicPage.getByRole('link', { name: '上级目录', exact: true }).click()
   await search.fill('')
   for (const width of [1024, 736, 375]) {
    await publicPage.setViewportSize({ width, height: 1000 })
    expect(await publicPage.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await expect(rows.first()).toBeVisible()
    await publicPage.screenshot({ path: testInfo.outputPath(`public-directory-${width}.png`), fullPage: true })
   }
   await publicPage.setViewportSize({ width: 1440, height: 1000 })
   await publicPage.getByRole('link', { name: 'packages', exact: true }).click()
   await expect(rows).toHaveCount(50)
   await expect(publicPage.getByRole('status')).toHaveText('1–50 / 55 项')
   await publicPage.getByRole('button', { name: '下一页', exact: true }).click()
   await expect(rows).toHaveCount(5)
   await expect(rows.first()).toContainText('package-051.rpm')
   await publicPage.getByRole('button', { name: '上一页', exact: true }).click()
   await publicPage.getByRole('button', { name: '大小', exact: false }).click()
   await expect(rows.first()).toContainText('package-001.rpm')
   await publicPage.getByRole('button', { name: '大小', exact: false }).click()
   await expect(rows.first()).toContainText('package-055.rpm')
   await search.fill('package-001')
   await expect(rows).toHaveCount(1)
   await expect(publicPage.locator('.pagination')).toBeHidden()
   await publicPage.getByRole('link', { name: '上级目录', exact: true }).click()
   await publicPage.getByRole('link', { name: 'releases', exact: true }).click()
   await expect(publicPage.getByRole('heading', { name: '目录为空' })).toBeVisible()
   await expect(publicPage.getByRole('button', { name: '清除筛选' })).toBeHidden()
  } finally { await publicContext.close() }
  const noJS = await browser.newContext({ javaScriptEnabled: false })
  try {
   const fallback = await noJS.newPage()
   await fallback.goto(base + '/github/mirror-client/packages/')
   await expect(fallback.locator('#file-rows tr')).toHaveCount(55)
   await expect(fallback.getByRole('link', { name: 'package-055.rpm', exact: true })).toBeVisible()
  } finally { await noJS.close() }
  expect(errors).toEqual([])
 } finally { await stop(app); await stop(daemon); await rm(dir, { recursive: true, force: true }) }
})
