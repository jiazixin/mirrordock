import { test, expect, type Page } from '@playwright/test'
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
  await new Promise<void>((resolve) => {
    const timer = setTimeout(() => {
      child.kill('SIGKILL')
      resolve()
    }, 5000)
    child.once('exit', () => {
      clearTimeout(timer)
      resolve()
    })
    child.kill('SIGTERM')
  })
}

async function expectHeaderLayout(page: Page) {
  const heading = await page.locator('.directory-heading').boundingBox()
  const toolbar = await page.locator('.list-heading').boundingBox()
  const panel = await page.locator('.file-panel').boundingBox()
  const title = await page.locator('#files-heading').boundingBox()
  const search = await page.locator('.search').boundingBox()
  expect(heading && toolbar && panel && title && search).toBeTruthy()
  for (const box of [heading!, toolbar!]) {
    expect(Math.abs(box.x - panel!.x)).toBeLessThan(1)
    expect(Math.abs(box.width - panel!.width)).toBeLessThan(1)
  }
  expect(panel!.y - (toolbar!.y + toolbar!.height)).toBe(14)
  expect(search!.x + search!.width).toBeLessThanOrEqual(panel!.x + panel!.width + 1)
  if (page.viewportSize()!.width <= 800) {
    expect(search!.y).toBeGreaterThanOrEqual(title!.y + title!.height)
    expect(search!.width).toBeCloseTo(panel!.width, 0)
  } else {
    expect(search!.x).toBeGreaterThan(title!.x + title!.width)
    expect(panel!.y - heading!.y).toBeLessThanOrEqual(140)
  }
}

test('public directory layout, icons, search, sorting, unpaginated and safe file links', async ({
  page,
  request,
  browser,
}, testInfo) => {
  const rsync = process.env.MIRRORDOCK_TEST_RSYNC
  test.skip(!rsync, 'Set MIRRORDOCK_TEST_RSYNC to a modern rsync executable')
  const root = path.resolve(import.meta.dirname, '../..')
  const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-directory-'))
  const port = await freePort(),
    daemonPort = await freePort()
  const base = `http://127.0.0.1:${port}`
  const upstream = path.join(dir, 'upstream')
  await mkdir(path.join(upstream, 'packages'), { recursive: true })
  await mkdir(path.join(upstream, 'releases'))
  await mkdir(path.join(upstream, 'docs'))
  const files = [
    'mirror-client-2.4.0.x86_64.rpm',
    'mirror-client_2.4.0_amd64.deb',
    'source-2.4.0.tar.gz',
    'source-2.4.0.tar.xz',
    'assets.zip',
    'manifest.json',
    'repository.yaml',
    'SHA256SUMS',
    'SHA256SUMS.asc',
    'RPM-GPG-KEY-mirror',
    'installer.iso',
    'README.md',
  ]
  for (const [i, name] of files.entries())
    await writeFile(path.join(upstream, name), Buffer.alloc((i + 1) * 1024, 65))
  const hostile = '<img src=x onerror=alert(1)>.json'
  await writeFile(path.join(upstream, 'docs', hostile), 'escaped filename')
  for (let i = 1; i <= 55; i++)
    await writeFile(
      path.join(upstream, 'packages', `package-${String(i).padStart(3, '0')}.rpm`),
      Buffer.alloc(i * 128, 66),
    )
  await writeFile(
    path.join(dir, 'rsyncd.conf'),
    `use chroot = no\nuid =\ngid =\nmunge symlinks = no\n[repo]\npath = ${upstream}\nread only = yes\n`,
  )
  const daemon = spawn(
    rsync!,
    [
      '--daemon',
      '--no-detach',
      '--address=127.0.0.1',
      `--port=${daemonPort}`,
      `--config=${path.join(dir, 'rsyncd.conf')}`,
    ],
    { stdio: 'ignore' },
  )
  const configFile = path.join(dir, 'config.yaml')
  await writeFile(
    configFile,
    `listen: 127.0.0.1\nport: ${port}\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: mirror-admin\n  password: isolated directory password 123\nsources:\n  - id: isolated\n    name: 文件列表测试\n    type: rsync\n    enabled: true\n    src1: rsync://127.0.0.1:${daemonPort}/repo/\n    dest: github/mirror-client\n`,
  )
  const app = spawn(
    process.env.MIRRORDOCK_TEST_BINARY ?? path.join(root, 'bin', 'mirrordock'),
    ['--config', configFile],
    {
      env: {
        ...process.env,
        TZ: 'Asia/Shanghai',
        PATH: `${path.dirname(rsync!)}${path.delimiter}${process.env.PATH}`,
      },
      stdio: 'ignore',
    },
  )
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  try {
    await expect
      .poll(async () => {
        try {
          return (await request.get(base + '/api/auth/status')).status()
        } catch {
          return 0
        }
      })
      .toBe(200)
    await page.goto(base + '/admin/')
    await page.getByLabel('用户名', { exact: true }).fill('mirror-admin')
    await page.getByLabel('管理员密码').fill('isolated directory password 123')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await page
      .getByRole('row')
      .filter({ hasText: '文件列表测试' })
      .getByRole('button', { name: '同步', exact: true })
      .click()
    await expect
      .poll(
        async () => (await request.get(base + '/github/mirror-client/manifest.json')).status(),
        { timeout: 20000 },
      )
      .toBe(200)
    // A fresh browser context proves that public browsing does not require login.
    const publicContext = await browser.newContext({ viewport: { width: 1440, height: 1250 } })
    try {
      const publicPage = await publicContext.newPage()
      publicPage.on('pageerror', (error) => errors.push(error.message))
      publicPage.on('dialog', async (dialog) => {
        errors.push(`unexpected dialog: ${dialog.message()}`)
        await dialog.dismiss()
      })
      await publicPage.goto(base + '/')
      const navigation = publicPage.getByRole('navigation', { name: '目录路径' })
      await expect(navigation.locator('[aria-current="page"]')).toHaveText('首页')
      await expect(navigation.locator('.home-icon')).toBeVisible()
      await expect(navigation.getByRole('link')).toHaveCount(0)
      await expect(
        publicPage.locator(
          '.brand-caption, .public-label, .parent-link, .directory-slash, .action',
        ),
      ).toHaveCount(0)
      await publicPage.getByRole('link', { name: 'github', exact: true }).click()
      await expect(publicPage.locator('.project-table')).toBeVisible()
      await expect(publicPage.locator('#file-rows')).toContainText('同步成功')
      await expect(publicPage.getByRole('button', { name: '上次同步', exact: false })).toBeVisible()
      await publicPage.getByRole('link', { name: 'mirror-client', exact: true }).click()
      await expect(navigation.locator('li')).toHaveText(['首页', 'github', 'mirror-client'])
      await expect(navigation.getByRole('link', { name: 'github', exact: true })).toHaveAttribute(
        'href',
        '/github/',
      )
      await expect(navigation.locator('[aria-current="page"]')).toHaveText('mirror-client')
      await expect(
        navigation.getByRole('link', { name: 'mirror-client', exact: true }),
      ).toHaveCount(0)
      await expect(publicPage.getByRole('link', { name: '上级目录', exact: true })).toHaveCount(0)
      await expect(publicPage.locator('.location-label')).toHaveText('当前位置')
      const rows = publicPage.locator('#file-rows tr:visible')
      await expect(rows).toHaveCount(15)
      await expect(rows.first()).toContainText('docs')
      await expect(publicPage.locator('.file-icon.folder')).toHaveCount(3)
      await expect(publicPage.locator('.file-icon.package')).toHaveCount(2)
      await expect(publicPage.locator('.file-icon.archive')).toHaveCount(3)
      await expect(rows.filter({ hasText: 'manifest.json' })).toContainText('6.0 KiB')
      await expect(publicPage.locator('time').first()).toHaveAttribute('datetime', /\+08:00$/)
      await publicPage.screenshot({
        path: testInfo.outputPath('public-directory-desktop.png'),
        fullPage: true,
      })
      await expect(publicPage.locator('.filters')).toHaveCount(0)
      await expect(publicPage.locator('.page-footer')).toHaveCount(0)
      await expect(publicPage.getByText('浏览目录，获取你需要的软件与文件。')).toHaveCount(0)
      const search = publicPage.getByRole('searchbox', { name: '搜索当前目录' })
      await search.fill('does-not-exist')
      await expect(publicPage.getByRole('heading', { name: '没有匹配的文件' })).toBeVisible()
      await publicPage.getByRole('button', { name: '清除搜索' }).click()
      await expect(rows).toHaveCount(15)
      await publicPage.getByRole('link', { name: 'docs', exact: true }).click()
      await search.fill('<img')
      await expect(rows).toHaveCount(1)
      await expect(rows.first()).toContainText(hostile)
      expect(await publicPage.locator('img').count()).toBe(0)
      const download = await publicContext.request.get(
        base + '/github/mirror-client/docs/' + encodeURIComponent(hostile),
      )
      expect(download.status()).toBe(200)
      expect(await download.text()).toBe('escaped filename')
      await navigation.getByRole('link', { name: 'mirror-client', exact: true }).click()
      await search.fill('')
      for (const width of [1024, 736, 375, 320]) {
        await publicPage.setViewportSize({ width, height: 1000 })
        expect(
          await publicPage.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
        ).toBe(true)
        await expect(rows.first()).toBeVisible()
        await expectHeaderLayout(publicPage)
        await publicPage.screenshot({
          path: testInfo.outputPath(`public-directory-${width}.png`),
          fullPage: true,
        })
      }
      await publicPage.setViewportSize({ width: 1440, height: 1000 })
      await publicPage.getByRole('link', { name: 'packages', exact: true }).click()
      await expect(rows).toHaveCount(55)
      await expect(publicPage.getByRole('status')).toHaveText('55 个条目')
      await expect(publicPage.locator('.pagination')).toHaveCount(0)
      await publicPage.getByRole('button', { name: '大小', exact: false }).click()
      await expect(rows.first()).toContainText('package-001.rpm')
      await publicPage.getByRole('button', { name: '大小', exact: false }).click()
      await expect(rows.first()).toContainText('package-055.rpm')
      await search.fill('package-001')
      await expect(rows).toHaveCount(1)
      await expect(publicPage.locator('.pagination')).toHaveCount(0)
      await navigation.getByRole('link', { name: 'mirror-client', exact: true }).click()
      await publicPage.getByRole('link', { name: 'releases', exact: true }).click()
      await expect(publicPage.getByRole('heading', { name: '目录为空' })).toBeVisible()
      await expect(publicPage.getByRole('button', { name: '清除搜索' })).toBeHidden()
      await navigation.getByRole('link', { name: '首页', exact: true }).click()
      await expect(publicPage).toHaveURL(base + '/')
      await expect(navigation.locator('[aria-current="page"]')).toHaveText('首页')
    } finally {
      await publicContext.close()
    }
    const noJS = await browser.newContext({ javaScriptEnabled: false })
    try {
      const fallback = await noJS.newPage()
      await fallback.goto(base + '/github/mirror-client/packages/')
      await expect(fallback.locator('#file-rows tr')).toHaveCount(55)
      await fallback
        .getByRole('navigation', { name: '目录路径' })
        .getByRole('link', { name: 'mirror-client', exact: true })
        .click()
      await expect(fallback).toHaveURL(base + '/github/mirror-client/')
      await fallback.goto(base + '/github/mirror-client/packages/')
      await expect(
        fallback.getByRole('link', { name: 'package-055.rpm', exact: true }),
      ).toBeVisible()
    } finally {
      await noJS.close()
    }
    expect(errors).toEqual([])
  } finally {
    await stop(app)
    await stop(daemon)
    await rm(dir, { recursive: true, force: true })
  }
})

test('GitHub project shows sync metadata and release rows show sizes', async ({
  page,
  request,
  browser,
}, testInfo) => {
  const root = path.resolve(import.meta.dirname, '../..')
  const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-release-list-'))
  const port = await freePort()
  const base = `http://127.0.0.1:${port}`
  const repository = path.join(dir, 'mirror', 'github', 'openlist')
  const synced = '2026-09-30T06:00:00Z'
  for (const [tag, size] of [
    ['v1', 2048],
    ['v2', 4096],
  ] as const) {
    await mkdir(path.join(repository, tag), { recursive: true })
    await writeFile(path.join(repository, tag, 'asset.zip'), Buffer.alloc(size, 65))
  }
  await writeFile(path.join(repository, 'v1', '.mirrordock-release-sync'), synced)
  const deepSegments = [
    'linux',
    'amd64',
    'packages',
    '目录 & 编码',
    'a-very-long-parent-directory-name'.repeat(3),
    'current-directory-name'.repeat(8),
  ]
  await mkdir(path.join(repository, 'v1', ...deepSegments), { recursive: true })
  const configFile = path.join(dir, 'config.yaml')
  await writeFile(
    configFile,
    `listen: 127.0.0.1
port: ${port}
path: ./data
mirror_root: ./mirror
auth:
  username: isolated
  password: isolated release test password
sources:
  - id: releases
    name: OpenList
    type: github_release
    enabled: true
    dest: github/openlist
    github_release:
      repository: owner/repo
    schedule:
      enabled: true
      cron: '0 0 1 1 *'
`,
  )
  const app = spawn(
    process.env.MIRRORDOCK_TEST_BINARY ?? path.join(root, 'bin', 'mirrordock'),
    ['--config', configFile],
    { env: { ...process.env, TZ: 'Asia/Shanghai' }, stdio: 'ignore' },
  )
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  try {
    await expect
      .poll(async () => {
        try {
          return (await request.get(base + '/api/auth/status')).status()
        } catch {
          return 0
        }
      })
      .toBe(200)
    await page.goto(base + '/github/')
    await expect(page.locator('.project-table')).toBeVisible()
    const projectRow = page.locator('#file-rows tr').filter({ hasText: 'openlist' })
    await expect(projectRow.locator('.size')).toHaveText('6.0 KiB')
    await expect(projectRow.locator('.status-badge')).toHaveText('已发布')
    await expect(projectRow.locator('.sync-time').first()).toHaveText('—')
    const projectNext = await projectRow
      .locator('.sync-time')
      .last()
      .locator('time')
      .getAttribute('datetime')
    expect(projectNext).toMatch(/\+08:00$/)
    for (const width of [1440, 375, 320]) {
      await page.setViewportSize({ width, height: 900 })
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      )
      await expect(projectRow.locator('.status-badge')).toBeVisible()
      await expectHeaderLayout(page)
      await page.screenshot({
        path: testInfo.outputPath(`github-project-${width}.png`),
        fullPage: true,
      })
    }
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.getByRole('link', { name: 'openlist', exact: true }).click()
    await expect(page.locator('.project-table')).toHaveCount(0)
    const rows = page.locator('#file-rows tr:visible')
    await expect(rows).toHaveCount(2)
    await expect(rows.filter({ hasText: 'v1' })).toContainText('2.0 KiB')
    await expect(rows.filter({ hasText: 'v2' })).toContainText('4.0 KiB')
    await expect(page.locator('.sync-time, .status-badge')).toHaveCount(0)
    await expect(page.getByRole('button', { name: '上次同步', exact: false })).toHaveCount(0)
    await expect(page.getByRole('button', { name: '下次同步', exact: false })).toHaveCount(0)
    const search = page.getByRole('searchbox', { name: '搜索当前目录' })
    await search.fill('v2')
    await expect(rows).toHaveCount(1)
    await search.fill('')
    await page.getByRole('button', { name: '大小', exact: false }).click()
    await page.getByRole('button', { name: '大小', exact: false }).click()
    await expect(rows.first()).toContainText('v2')
    for (const width of [1440, 1024, 736, 375, 320]) {
      await page.setViewportSize({ width, height: 900 })
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      )
      await expect(rows.first().locator('.size')).toBeVisible()
      await expectHeaderLayout(page)
      await page.screenshot({
        path: testInfo.outputPath(`release-list-${width}.png`),
        fullPage: true,
      })
    }
    await page.getByRole('link', { name: 'v1', exact: true }).click()
    await expect(page.locator('.project-table')).toHaveCount(0)
    await expect(page.getByRole('link', { name: 'asset.zip', exact: true })).toBeVisible()
    expect(
      (await request.get(base + '/github/openlist/v1/.mirrordock-release-sync')).status(),
    ).toBe(404)
    const segments = ['github', 'openlist', 'v1', ...deepSegments]
    const deepPath = '/' + segments.map(encodeURIComponent).join('/') + '/'
    await page.goto(base + deepPath)
    const navigation = page.getByRole('navigation', { name: '目录路径' })
    const current = navigation.locator('[aria-current="page"]')
    await expect(current).toHaveText(segments.at(-1)!)
    await expect(navigation.getByRole('link')).toHaveCount(segments.length)
    for (const [i, segment] of segments.slice(0, -1).entries()) {
      const link = navigation.getByRole('link', { name: segment, exact: true })
      expect(
        decodeURIComponent(new URL((await link.getAttribute('href')) ?? '', base).pathname),
      ).toBe('/' + segments.slice(0, i + 1).join('/') + '/')
    }
    for (const width of [1440, 1024, 736, 375, 320]) {
      await page.setViewportSize({ width, height: 900 })
      await expect
        .poll(async () => {
          const navBox = await navigation.boundingBox(),
            currentBox = await current.boundingBox()
          return (
            !!navBox &&
            !!currentBox &&
            currentBox.x >= navBox.x - 1 &&
            currentBox.x + currentBox.width <= navBox.x + navBox.width + 1
          )
        })
        .toBe(true)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      )
      await expectHeaderLayout(page)
      await page.screenshot({
        path: testInfo.outputPath(`deep-header-${width}.png`),
        fullPage: true,
      })
    }
    await navigation.getByRole('link', { name: '目录 & 编码', exact: true }).click()
    expect(decodeURIComponent(new URL(page.url()).pathname)).toBe(
      '/' + segments.slice(0, 7).join('/') + '/',
    )
    await navigation.getByRole('link', { name: '首页', exact: true }).click()
    await expect(page).toHaveURL(base + '/')
    const noJS = await browser.newContext({
      javaScriptEnabled: false,
      viewport: { width: 320, height: 900 },
    })
    try {
      const fallback = await noJS.newPage()
      await fallback.goto(base + deepPath)
      await expect(fallback.getByRole('navigation').locator('[aria-current="page"]')).toHaveText(
        segments.at(-1)!,
      )
      expect(
        await fallback.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      ).toBe(true)
      await fallback
        .getByRole('navigation')
        .getByRole('link', { name: 'openlist', exact: true })
        .click()
      await expect(fallback).toHaveURL(base + '/github/openlist/')
    } finally {
      await noJS.close()
    }
    expect(errors).toEqual([])
  } finally {
    await stop(app)
    await rm(dir, { recursive: true, force: true })
  }
})
