import { test, expect } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { mkdtemp, writeFile, readFile, rm, mkdir, appendFile } from 'node:fs/promises'
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
  if (child.exitCode !== null) return
  child.kill('SIGTERM')
  await Promise.race([
    new Promise((resolve) => child.once('exit', resolve)),
    new Promise((resolve) =>
      setTimeout(() => {
        child.kill('SIGKILL')
        resolve(null)
      }, 5000),
    ),
  ])
}

test('real configuration, rsync failover, publication, records and responsive UI', async ({
  page,
  request,
}, testInfo) => {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  const root = path.resolve(import.meta.dirname, '../..')
  const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-e2e-'))
  const port = await freePort()
  const daemonPort = await freePort()
  const missingPort = await freePort()
  const base = `http://127.0.0.1:${port}`
  const upstream = path.join(dir, 'upstream')
  await mkdir(upstream)
  await writeFile(path.join(upstream, 'package.rpm'), 'real package from isolated upstream')
  const rsync = process.env.MIRRORDOCK_TEST_RSYNC
  test.skip(!rsync, 'Set MIRRORDOCK_TEST_RSYNC to a modern rsync executable')
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
    `# e2e comment\nlisten: 127.0.0.1\nport: ${port}\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: mirror-admin\n  password: strong local password 123\nsettings:\n  task_timeout: 30s\n  rsync:\n    connect_timeout: 2s\n    io_timeout: 2s\nsources: []\n`,
  )
  let output = ''
  const app = spawn(
    process.env.MIRRORDOCK_TEST_BINARY ?? path.join(root, 'bin', 'mirrordock'),
    ['--config', configFile],
    {
      env: {
        ...process.env,
        TZ: 'Asia/Shanghai',
        PATH: `${path.dirname(rsync!)}${path.delimiter}${process.env.PATH}`,
      },
      stdio: ['ignore', 'ignore', 'pipe'],
    },
  )
  app.stderr!.on('data', (chunk) => {
    output += String(chunk)
  })
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
    expect(output).not.toContain('初始化码')
    expect(output).not.toContain('strong local password 123')
    await page.goto(base + '/admin/')
    await expect(page.getByLabel('初始化码')).toHaveCount(0)
    await page.getByLabel('用户名', { exact: true }).fill('wrong-user')
    await page.getByLabel('管理员密码').fill('strong local password 123')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await expect(page.getByRole('alert')).toContainText('用户名或密码错误')
    await page.getByLabel('用户名', { exact: true }).fill('mirror-admin')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await expect(page).toHaveURL(base + '/admin/overview')
    await expect(page.getByRole('heading', { name: '同步时间轴' })).toBeVisible()
    await page.getByRole('link', { name: '同步源', exact: true }).click()
    await expect(page.getByRole('heading', { name: '同步源', exact: true })).toBeVisible()
    await expect(page.locator('.account strong')).toHaveText('mirror-admin')
    await expect(page.locator('.topbar')).toHaveCount(0)
    await expect(page.getByText('LOCAL MIRROR', { exact: true })).toHaveCount(0)
    await page.getByRole('button', { name: '新增同步源' }).first().click()
    await expect(page.locator('select')).toHaveCount(0)
    const engine = page.getByRole('combobox', { name: '同步方式', exact: true })
    await engine.click()
    await expect(page.getByRole('listbox')).toBeVisible()
    await engine.press('Escape')
    await expect(page.getByRole('listbox')).toHaveCount(0)
    await expect(page.getByRole('dialog')).toBeVisible()
    await engine.click()
    await page.getByRole('option', { name: 'GitHub Release', exact: true }).click()
    await expect(page.getByLabel('GitHub 仓库')).toBeVisible()
    await engine.press('Home')
    await engine.press('Enter')
    await expect(engine).toHaveText('Rsync')
    await expect(page.getByLabel('GitHub 仓库')).toHaveCount(0)
    await page.getByLabel('源名称').fill('隔离测试源')
    await page.getByLabel('唯一标识').fill('isolated')
    await page.getByLabel('目的目录').fill('docker-ce')
    await page
      .getByPlaceholder('rsync://mirror.example/repo/')
      .fill(`rsync://127.0.0.1:${missingPort}/repo/`)
    await page.getByRole('button', { name: '添加备用来源' }).click()
    await page
      .getByPlaceholder('rsync://mirror.example/repo/')
      .nth(1)
      .fill(`rsync://127.0.0.1:${daemonPort}/repo/`)
    await page.getByRole('button', { name: '保存并写回 YAML' }).click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    const row = page.getByRole('row').filter({ hasText: '隔离测试源' })
    await expect(row).toBeVisible()
    await expect(page.locator('.source-table th')).toHaveText([
      '同步源',
      '来源',
      '目标',
      '最近运行',
      '状态',
      '操作',
    ])
    await expect(row.locator('td').nth(1)).toContainText(`rsync://127.0.0.1:${missingPort}/repo/`)
    await expect(row.locator('td').nth(2)).toHaveText('/docker-ce/')
    await expect(row.locator('td').nth(3)).toHaveText('—')
    await expect(row.locator('td').nth(4)).toHaveText('尚未运行')
    await row.getByRole('button', { name: '同步 隔离测试源', exact: true }).click()
    await page.getByRole('link', { name: '批次记录' }).click()
    await expect(page.locator('.run-row').first().getByText('成功', { exact: true })).toBeVisible({
      timeout: 20000,
    })
    await expect(page.locator('.run-row').first()).toContainText('已尝试 2 次')
    for (const [name, option] of [
      ['时间范围', '最近 24 小时'],
      ['同步源', '隔离测试源'],
      ['运行来源', '手动触发'],
    ]) {
      await page.getByRole('combobox', { name, exact: true }).click()
      await page.getByRole('option', { name: option, exact: true }).click()
      await expect(page.locator('.run-row')).toHaveCount(1)
    }
    const origin = page.getByRole('combobox', { name: '运行来源', exact: true })
    await origin.click()
    await page.getByRole('option', { name: '自动计划', exact: true }).click()
    await expect(page.locator('.run-row')).toHaveCount(0)
    await origin.press('Home')
    await origin.press('Enter')
    const status = page.getByRole('combobox', { name: '批次状态', exact: true })
    await status.click()
    await page.getByRole('option', { name: '失败', exact: true }).click()
    await expect(page.locator('.run-row')).toHaveCount(0)
    await status.press('Home')
    await status.press('Enter')
    await expect(page.locator('.run-row')).toHaveCount(1)
    await status.click()
    await page.waitForTimeout(1200)
    await expect(page.getByRole('listbox')).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath('custom-dropdown.png'), fullPage: true })
    await page.getByRole('heading', { name: '批次记录' }).click()
    await expect(page.getByRole('listbox')).toHaveCount(0)
    const download = await request.get(base + '/docker-ce/package.rpm')
    expect(download.status()).toBe(200)
    expect(await download.text()).toBe('real package from isolated upstream')
    const partial = await request.get(base + '/docker-ce/package.rpm', {
      headers: { Range: 'bytes=0-3' },
    })
    expect(partial.status()).toBe(206)
    expect(await partial.text()).toBe('real')
    await page.screenshot({ path: testInfo.outputPath('batches.png'), fullPage: true })
    await page.getByRole('button', { name: '查看运行详情' }).first().click()
    await expect(page.getByRole('dialog')).toContainText('UPSTREAM_ERROR')
    await page.locator('summary').filter({ hasText: '原始日志' }).click()
    const attempt = page.getByRole('combobox', { name: '选择日志尝试' })
    await expect(attempt).toHaveText('尝试 #2')
    await attempt.click()
    await page.getByRole('option', { name: '尝试 #1', exact: true }).click()
    await expect(attempt).toHaveText('尝试 #1')
    await expect(page.locator('.log-view')).toContainText('Connection refused')
    await attempt.press('End')
    await attempt.press('Enter')
    await expect(attempt).toHaveText('尝试 #2')
    await expect(page.locator('.log-view')).toContainText('Total transferred file size: 35 bytes')
    let staleRequested = false
    await page.route('**/api/runs/*/logs?attempt=1&offset=0', async (route) => {
      staleRequested = true
      await new Promise((resolve) => setTimeout(resolve, 600))
      await route.fulfill({ json: { text: '过期尝试响应', next_offset: 10 } })
    })
    await attempt.click()
    await page.getByRole('option', { name: '尝试 #1', exact: true }).click()
    await expect.poll(() => staleRequested).toBe(true)
    await attempt.click()
    await page.getByRole('option', { name: '尝试 #2', exact: true }).click()
    await expect(page.locator('.log-view')).toContainText('Total transferred file size: 35 bytes')
    await page.waitForTimeout(700)
    await expect(page.locator('.log-view')).not.toContainText('过期尝试响应')
    await page.unroute('**/api/runs/*/logs?attempt=1&offset=0')
    await expect(page.getByRole('button', { name: '读取后续日志', exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: '下载', exact: true })).toBeVisible()
    const successful = await (await page.request.get(base + '/api/runs?state=succeeded')).json()
    const runID = successful.items[0].id
    const logFile = path.join(dir, 'data', 'logs', String(runID), '2.log')
    const fullLog = '完整日志起点\n' + '0123456789abcdef\n'.repeat(140000) + '完整日志终点\n'
    await appendFile(logFile, fullLog)
    // Completed tasks no longer poll; an external append requires explicit refresh.
    await page.getByRole('button', { name: '刷新日志', exact: true }).click()
    await expect(page.locator('.log-view')).toContainText('完整日志终点', { timeout: 12000 })
    await expect(page.locator('.log-view')).not.toContainText('完整日志起点')
    const beforeDownload = await readFile(logFile, 'utf8')
    expect(
      (await request.get(base + `/api/runs/${runID}/logs?attempt=2&download=1`)).status(),
    ).toBe(401)
    await page.route('**/api/runs/*/logs?attempt=2&download=1', async (route) => {
      const response = await route.fetch()
      await appendFile(logFile, '下载请求后追加\n')
      await route.fulfill({ response })
    })
    const downloadEvent = page.waitForEvent('download')
    await page.getByRole('button', { name: '下载', exact: true }).click()
    const logDownload = await downloadEvent
    expect(logDownload.suggestedFilename()).toBe(`run-${runID}-attempt-2.log`)
    expect(await readFile((await logDownload.path())!, 'utf8')).toBe(beforeDownload)
    await page.unroute('**/api/runs/*/logs?attempt=2&download=1')
    let logRequests = 0
    page.on('request', (req) => {
      if (req.url().includes(`/api/runs/${runID}/logs`)) logRequests++
    })
    await page.getByRole('button', { name: '关闭', exact: true }).click()
    const stopped = logRequests
    await page.waitForTimeout(3300)
    expect(logRequests).toBe(stopped)
    await page.getByRole('link', { name: '运行记录' }).click()
    await expect(page.locator('.table-scroll table')).toContainText('隔离测试源')
    await page.getByRole('combobox', { name: '按同步源筛选' }).click()
    await page.getByRole('option', { name: '隔离测试源', exact: true }).click()
    await page.getByRole('combobox', { name: '按时间筛选' }).click()
    await page.getByRole('option', { name: '最近 24 小时' }).click()
    await expect(page.locator('.table-scroll table')).toContainText('隔离测试源')
    await page.screenshot({ path: testInfo.outputPath('records.png'), fullPage: true })
    await page.getByRole('link', { name: '系统设置' }).click()
    await expect(page.getByRole('tab', { name: 'Rsync', exact: true })).toHaveAttribute(
      'aria-selected',
      'true',
    )
    await expect(page.getByLabel('listen', { exact: true })).toHaveCount(0)
    await expect(page.getByLabel('port', { exact: true })).toHaveCount(0)
    await expect(page.getByLabel('证书链 · cert')).toHaveCount(0)
    await expect(page.getByLabel('私钥 · key')).toHaveCount(0)
    await page.getByRole('tab', { name: 'Rsync', exact: true }).click()
    await page.getByLabel('Rsync 传输限速').fill('12.5')
    await page.getByRole('tab', { name: 'Rsync', exact: true }).press('ArrowRight')
    await expect(page.getByRole('tab', { name: 'GitHub Release', exact: true })).toBeFocused()
    await page.getByLabel('GitHub Token', { exact: true }).fill('e2e-github-token')
    await page
      .getByLabel('GitHub 代理 · proxy')
      .fill('socks5://user:e2e-proxy-password@127.0.0.1:1080')
    await page.getByRole('button', { name: '保存设置' }).click()
    await expect.poll(async () => await readFile(configFile, 'utf8')).toContain('limit_mb: 12.5')
    expect(await readFile(configFile, 'utf8')).toContain('# e2e comment')
    expect(await readFile(configFile, 'utf8')).toContain(
      'socks5://user:e2e-proxy-password@127.0.0.1:1080',
    )
    await expect(page.getByLabel('GitHub 代理 · proxy')).toHaveValue(
      'socks5://user:e2e-proxy-password@127.0.0.1:1080',
    )
    await expect(page.getByLabel('GitHub 代理 · proxy')).toHaveAttribute('type', 'text')
    const proxyView = await (await page.request.get(base + '/api/config')).json()
    expect(proxyView.token_configured).toBe(true)
    expect(proxyView.config.settings.github.token).toBe('')
    expect(JSON.stringify(proxyView)).not.toContain('e2e-github-token')
    expect(await readFile(configFile, 'utf8')).toContain('token: e2e-github-token')
    expect(proxyView.proxy_configured).toBe(true)
    expect(proxyView.config.settings.github.proxy).toBe(
      'socks5://user:e2e-proxy-password@127.0.0.1:1080',
    )
    expect(proxyView.config.auth.username).toBe('mirror-admin')
    expect(proxyView.config.auth).not.toHaveProperty('password')
    expect(await readFile(configFile, 'utf8')).toContain('password: strong local password 123')
    expect(JSON.stringify(proxyView)).not.toContain('strong local password 123')
    await page.screenshot({ path: testInfo.outputPath('settings-github.png'), fullPage: true })
    await page.reload()
    await page.getByRole('tab', { name: 'GitHub Release', exact: true }).click()
    await expect(page.getByLabel('GitHub 代理 · proxy')).toHaveValue(
      'socks5://user:e2e-proxy-password@127.0.0.1:1080',
    )
    await page.getByLabel('GitHub 代理 · proxy').fill('')
    await page.getByRole('button', { name: '保存设置' }).click()
    await expect
      .poll(
        async () => (await (await page.request.get(base + '/api/config')).json()).proxy_configured,
      )
      .toBe(false)
    await expect(page.locator('.config-footer')).toHaveCount(0)
    await expect(page.getByRole('link', { name: '系统配置', exact: true })).toHaveCount(0)
    await expect(page.getByRole('tab')).toHaveText([
      'Rsync',
      'GitHub Release',
      '同步策略',
      '系统配置',
      '通知设置',
    ])
    await page.getByRole('tab', { name: '同步策略', exact: true }).click()
    await expect(page.getByLabel('失败重试次数')).toHaveValue('3')
    await page.getByLabel('失败重试次数').fill('0')
    await page.getByRole('button', { name: '保存设置' }).click()
    await expect
      .poll(
        async () =>
          (await (await page.request.get(base + '/api/config')).json()).config.settings.retry_count,
      )
      .toBe(0)
    expect(await readFile(configFile, 'utf8')).toContain('retry_count: 0')
    await page.reload()
    await page.getByRole('tab', { name: '同步策略', exact: true }).click()
    await expect(page.getByLabel('失败重试次数')).toHaveValue('0')
    await page.getByLabel('失败重试次数').fill('3')
    await page.getByRole('button', { name: '保存设置' }).click()
    await expect
      .poll(
        async () =>
          (await (await page.request.get(base + '/api/config')).json()).config.settings.retry_count,
      )
      .toBe(3)
    await expect(page.getByRole('button', { name: '保存设置' })).toBeEnabled()
    await page.screenshot({ path: testInfo.outputPath('settings-retry.png'), fullPage: true })
    await page.getByRole('tab', { name: '同步策略', exact: true }).press('ArrowRight')
    await expect(page.getByRole('tab', { name: '系统配置', exact: true })).toBeFocused()
    await expect(page.locator('.active-config')).toContainText(configFile)
    await expect(page.getByRole('heading', { name: '运行环境', exact: true })).toHaveCount(0)
    await expect(page.getByRole('heading', { name: '监听与存储', exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: '保存设置' })).toHaveCount(0)
    await expect(page.locator('.environment-checks')).toContainText('检查完成')
    await expect(
      page.locator('.check-row').filter({ has: page.getByText('Rsync', { exact: true }) }),
    ).toContainText('版本 3.')
    await expect(
      page.locator('.check-row').filter({ has: page.getByText('配置目录', { exact: true }) }),
    ).toContainText('可写')
    await expect(page.locator('pre')).toHaveCount(0)
    const checks = await (await page.request.get(base + '/api/system/checks')).json()
    const reflinkRow = page
      .locator('.check-row')
      .filter({ has: page.getByText('Reflink', { exact: true }) })
    expect(checks.reflink.status).toBe('supported')
    await expect(reflinkRow).toContainText('镜像目录实测支持')
    await expect(page.locator('.check-row').filter({ hasText: '镜像盘空间' })).toContainText(
      '安全余量',
    )
    for (const [status, detail, label] of [
      ['unsupported', '测试文件系统不支持克隆', '不支持'],
      ['error', '测试探测目录不可写', '探测失败'],
      ['supported', '测试克隆成功', '支持'],
    ]) {
      await page.route('**/api/system/checks', (route) =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ ...checks, reflink: { status, detail } }),
        }),
      )
      await page.getByRole('button', { name: '刷新', exact: true }).click()
      await expect(reflinkRow.locator('.check-status')).toHaveText(label)
      await expect(reflinkRow).toContainText(detail)
      await page.unroute('**/api/system/checks')
    }
    await page.route('**/api/system/checks', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ ...checks, rsync: { available: false, version: '' } }),
      }),
    )
    await page.getByRole('button', { name: '刷新', exact: true }).click()
    await expect(page.locator('.environment-checks')).toContainText('检查完成')
    await expect(
      page.locator('.check-row').filter({ has: page.getByText('Rsync', { exact: true }) }),
    ).toContainText('未检测到可用的 Rsync')
    await page.unroute('**/api/system/checks')
    await page.route('**/api/system/checks', (route) =>
      route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ error: '环境检查测试失败' }),
      }),
    )
    await page.getByRole('button', { name: '刷新', exact: true }).click()
    await expect(page.getByRole('alert')).toContainText('环境检查测试失败')
    await expect(page.locator('.environment-checks')).toContainText('检查未完成')
    await expect(page.locator('.checks-list')).toHaveCount(0)
    await page.unroute('**/api/system/checks')
    await page.getByRole('button', { name: '刷新', exact: true }).click()
    await expect(page.locator('.environment-checks')).toContainText('检查完成')
    await expect(
      page.locator('.check-row').filter({ has: page.getByText('Rsync', { exact: true }) }),
    ).toContainText('版本 3.')
    await page.screenshot({ path: testInfo.outputPath('settings-environment.png'), fullPage: true })
    const publicationBefore = (await (await page.request.get(base + '/api/system/info')).json())
      .publications[0].current.id
    const noChange = await (
      await page.request.post(base + '/api/sources/isolated/run', {
        headers: { 'X-MirrorDock-Request': '1' },
      })
    ).json()
    await expect
      .poll(
        async () =>
          (await (await page.request.get(base + `/api/runs/${noChange.id}`)).json()).state,
        { timeout: 15000 },
      )
      .toBe('succeeded')
    expect(
      (await (await page.request.get(base + '/api/system/info')).json()).publications[0].current.id,
    ).toBe(publicationBefore)
    await page.getByRole('link', { name: '同步源', exact: true }).click()
    await page.getByRole('button', { name: '编辑 隔离测试源' }).click()
    await expect(page.getByRole('dialog')).toContainText('编辑同步源')
    await page.getByRole('button', { name: '取消', exact: true }).click()
    for (const width of [1440, 1024, 736, 375]) {
      await page.setViewportSize({ width, height: 1000 })
      for (const name of ['sources', 'batches', 'records', 'schedules', 'settings']) {
        await page.goto(base + '/admin/' + name)
        await expect(page.locator('.page-heading')).toBeVisible()
        await expect(
          page.locator(
            '.page-heading p,.connection,.page-note,.filter-help,.panel-caption,.sidebar .brand small',
          ),
        ).toHaveCount(0)
        await expect(page.locator('select')).toHaveCount(0)
        await expect(page.locator('.topbar,.crumb,.topbar-right')).toHaveCount(0)
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
        ).toBeTruthy()
        if (name === 'schedules')
          await page.screenshot({
            path: testInfo.outputPath(`schedules-${width}.png`),
            fullPage: true,
          })
        if (name === 'settings') {
          for (const tab of ['Rsync', 'GitHub Release', '同步策略', '系统配置']) {
            await page.getByRole('tab', { name: tab, exact: true }).click()
            if (tab === '系统配置')
              await expect(page.locator('.environment-checks')).toContainText('检查完成')
            if (tab === '同步策略')
              await page.screenshot({
                path: testInfo.outputPath(`settings-retry-${width}.png`),
                fullPage: true,
              })
            expect(
              await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
            ).toBeTruthy()
          }
          await page.screenshot({
            path: testInfo.outputPath(`settings-${width}.png`),
            fullPage: true,
          })
        }
      }
    }
    await page.getByRole('button', { name: '打开导航' }).click()
    await expect(page.locator('.primary-nav')).toHaveClass(/open/)
    await page.getByRole('link', { name: '同步源', exact: true }).click()
    await expect(page.locator('.primary-nav')).not.toHaveClass(/open/)
    await expect(page.locator('.primary-nav')).toBeHidden()
    await expect(page.locator('.page-heading')).toContainText('同步源')
    await page.screenshot({ path: testInfo.outputPath('mobile-sources.png'), fullPage: true })
    await page.goto(base + '/admin/batches')
    await page.getByRole('combobox', { name: '批次状态', exact: true }).click()
    const bounds = await page.getByRole('listbox').boundingBox()
    expect(bounds).toBeTruthy()
    expect(bounds!.x).toBeGreaterThanOrEqual(0)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(375)
    expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(1000)
    await page.screenshot({ path: testInfo.outputPath('mobile-dropdown.png'), fullPage: true })
    await page.getByRole('combobox', { name: '批次状态', exact: true }).press('Tab')
    await expect(page.getByRole('listbox')).toHaveCount(0)
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.goto(base + '/admin/sources')
    const filter = page.getByRole('combobox', { name: '筛选同步方式' })
    await filter.click()
    await page.getByRole('option', { name: 'GitHub Release', exact: true }).click()
    await expect(page.locator('.source-name')).toHaveCount(0)
    await filter.press('Home')
    await filter.press('ArrowDown')
    await filter.press('Enter')
    await expect(page.locator('.source-name')).toHaveCount(1)
    const fonts = await page.evaluate(() => ({
      family: getComputedStyle(document.body).fontFamily,
      heading: getComputedStyle(document.querySelector('h1')!).fontWeight,
      button: getComputedStyle(document.querySelector('.heading-actions .button')!).fontWeight,
    }))
    expect(fonts.family).not.toContain('Avenir')
    expect(fonts.heading).toBe('700')
    expect(fonts.button).toBe('600')
    const icon = await request.get(base + '/favicon.ico?v=2')
    const adminIcon = await request.get(base + '/admin/favicon.ico?v=2')
    expect(icon.status()).toBe(200)
    expect(adminIcon.status()).toBe(200)
    const iconBytes = await icon.body()
    expect(iconBytes.equals(await adminIcon.body())).toBeTruthy()
    expect([...iconBytes.subarray(0, 6)]).toEqual([0, 0, 1, 0, 6, 0])
    expect(await page.locator('link[rel="icon"]').getAttribute('href')).toBe(
      '/admin/favicon.ico?v=2',
    )
    await page.screenshot({ path: testInfo.outputPath('sources.png'), fullPage: true })
    expect(errors).toEqual([])
    // Read-only display fixtures cover all status/type colors without starting external syncs.
    const displayConfig = await (await page.request.get(base + '/api/config')).json()
    const realRuns = await (await page.request.get(base + '/api/runs?limit=100')).json()
    const types = ['rsync', 'reposync', 'github_release']
    displayConfig.config.sources = types.map((type, index) => ({
      ...displayConfig.config.sources[0],
      id: `display-${index}`,
      name: ['AlmaLinux 9', 'NGINX Stable · EL9 · x86_64', 'OpenList Releases'][index],
      type,
      enabled: false,
      upstreams:
        type === 'rsync'
          ? displayConfig.config.sources[0].upstreams
          : type === 'reposync'
            ? { src1: 'https://nginx.org/packages/centos/9/x86_64/' }
            : {},
      dest: ['almalinux/9', 'nginx/el9/x86_64', 'github/openlist'][index],
      github_release: { repository: 'OpenListTeam/OpenList', release_count: 5, asset_patterns: [] },
    }))
    const states = [
      'succeeded',
      'running',
      'failed',
      'paused',
      'timed_out',
      'interrupted',
      'cancelled',
      'queued',
      'pausing',
    ]
    const displayRuns = states.map((state, index) => ({
      ...realRuns.items[0],
      id: 100 - index,
      source_id: `display-${index % 3}`,
      name: displayConfig.config.sources[index % 3].name,
      type: types[index % 3],
      state,
      elapsed_seconds: 120,
      progress: {
        ...realRuns.items[0].progress,
        phase: state === 'running' ? 'transfer' : 'complete',
        percent: state === 'running' ? 36 : 100,
        speed: state === 'running' ? 35000 : 0,
        eta_seconds: state === 'running' ? 240 : null,
        skipped: 2,
      },
    }))
    await page.route('**/api/config', (route) => route.fulfill({ json: displayConfig }))
    await page.route('**/api/runs?*', (route) =>
      route.fulfill({ json: { items: displayRuns, total: displayRuns.length } }),
    )
    await page.route('**/api/events', (route) =>
      route.fulfill({ contentType: 'text/event-stream', body: '' }),
    )
    await page.reload()
    await expect(page.locator('.source-name')).toHaveCount(3)
    await expect(page.locator('.source-table td:nth-child(4) .badge')).toHaveCount(0)
    await expect(page.locator('.source-table td:nth-child(5) .badge')).toHaveCount(3)
    const typeColors = await page.locator('.source-table .type-pill').evaluateAll((elements) =>
      elements.map((element) => ({
        color: getComputedStyle(element).color,
        background: getComputedStyle(element).backgroundColor,
      })),
    )
    // Source types are neutral labels; color is reserved for run anomalies.
    expect(new Set(typeColors.map((item) => item.color)).size).toBe(1)
    await expect(page.locator('.source-table .type-pill')).toHaveText([
      'Rsync',
      'Reposync',
      'GitHub Release',
    ])
    await page.screenshot({ path: testInfo.outputPath('sources-tags.png'), fullPage: true })
    await page.goto(base + '/admin/batches')
    await expect(page.locator('.run-row')).toHaveCount(9)
    for (const state of states)
      await expect(page.locator(`.run-row .badge.${state}`)).toHaveCount(1)
    await page.screenshot({ path: testInfo.outputPath('batches-tags.png'), fullPage: true })
    // Overview lanes follow configured sources; attention and live lists use each source's latest run.
    await page.goto(base + '/admin/overview')
    await expect(page.locator('.lane:not(.lane-head)')).toHaveCount(3)
    await expect(page.locator('.track .segment').first()).toBeAttached()
    await expect(page.locator('.attention-row')).toHaveCount(1)
    await expect(page.locator('.attention-row')).toContainText('OpenList Releases')
    await expect(page.locator('.live-run')).toHaveCount(2)
    await expect(page.locator('.queued-row')).toHaveCount(1)
    await expect(page.locator('.lane-next').filter({ hasText: '已停用' })).toHaveCount(3)
    await page.screenshot({ path: testInfo.outputPath('overview.png'), fullPage: true })
    for (const width of [1024, 736, 375, 320]) {
      await page.setViewportSize({ width, height: 1000 })
      for (const name of ['sources', 'batches', 'overview']) {
        await page.goto(base + '/admin/' + name)
        await expect(
          page.locator(name === 'overview' ? '.overview-hero' : '.page-heading'),
        ).toBeVisible()
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
        ).toBeTruthy()
        if (width === 375)
          await page.screenshot({
            path: testInfo.outputPath(`${name}-tags-mobile.png`),
            fullPage: true,
          })
      }
    }
    await page.unroute('**/api/config')
    await page.unroute('**/api/runs?*')
    await page.unroute('**/api/events')
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.goto(base + '/admin/sources')
    expect(errors).toEqual([])
    await page.getByRole('button', { name: '退出登录' }).click()
    await expect(page.getByLabel('用户名', { exact: true })).toBeVisible()
    await page.getByLabel('用户名', { exact: true }).fill('mirror-admin')
    await page.getByLabel('管理员密码').fill('strong local password 123')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await expect(page.locator('.account strong')).toHaveText('mirror-admin')
    expect(output).not.toContain('strong local password 123')
    expect(errors).toEqual([])
  } finally {
    await stop(app)
    await stop(daemon)
    await rm(dir, { recursive: true, force: true })
  }
})
