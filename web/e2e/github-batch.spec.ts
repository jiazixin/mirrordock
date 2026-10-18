import { test, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'

test('batch adds GitHub Release sources with preview, conflict checks and staggered cron', async ({
  page,
  request,
}) => {
  const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-github-batch-'))
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
  // The existing source is disabled with no schedule, so nothing reaches GitHub.
  await writeFile(
    config,
    `listen: 127.0.0.1
port: ${port}
path: ./state
mirror_root: ./mirror
auth:
  username: admin
  password: github-batch-fixture-password
sources:
  - id: openlist
    name: OpenList
    type: github_release
    enabled: false
    dest: github/openlist
    github_release:
      repository: OpenListTeam/OpenList
    schedule:
      enabled: false
      cron: "0 2 * * *"
`,
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
    await page.goto(`${base}/admin/sources`)
    await page.getByLabel('用户名', { exact: true }).fill('admin')
    await page.getByLabel('管理员密码').fill('github-batch-fixture-password')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await expect(page.getByRole('heading', { name: '同步源', exact: true })).toBeVisible()

    await page.getByRole('button', { name: '新增同步源' }).first().click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('switch', { name: '批量添加' })).toHaveCount(0)
    await dialog.getByRole('combobox', { name: '同步方式', exact: true }).click()
    await page.getByRole('option', { name: 'GitHub Release', exact: true }).click()
    await dialog.getByRole('switch', { name: '批量添加' }).check()
    await expect(dialog.getByRole('heading', { name: '批量新增同步源' })).toBeVisible()
    await expect(dialog.getByLabel('源名称')).toHaveCount(0)
    await expect(dialog.getByLabel('唯一标识', { exact: true })).toHaveCount(0)

    await dialog.getByLabel('Cron 表达式').fill('0 3 * * *')
    await dialog.getByRole('switch', { name: '启用自动计划' }).check()
    await dialog.getByLabel('共用附件 glob').fill('*.tar.gz')
    await dialog
      .getByLabel('GitHub 仓库')
      .fill(
        'OpenListTeam/OpenList\nhttps://github.com/cli/cli.git\nowner/cli, not-a-repo\n' +
          'openlistteam/openlist git@github.com:extra/tool.git ' +
          'https://github.com/CLI/cli/releases?tab=assets gitlab.com/group/project',
      )
    await expect(dialog.getByRole('status')).toContainText('not-a-repo')
    await expect(dialog.getByRole('status')).toContainText('gitlab.com/group/project')
    await expect(dialog.getByRole('heading', { name: /预览 4 个同步源/ })).toBeVisible()

    // The repo-name id is taken by the existing source, so owner-repo is used.
    const field = (repository: string, label: string) =>
      dialog.getByLabel(`${repository} ${label}`, { exact: true })
    await expect(field('OpenListTeam/OpenList', '唯一标识')).toHaveValue('openlistteam-openlist')
    await expect(field('OpenListTeam/OpenList', '目的目录')).toHaveValue(
      'github/openlistteam-openlist',
    )
    await expect(field('cli/cli', '唯一标识')).toHaveValue('cli')
    await expect(field('owner/cli', '唯一标识')).toHaveValue('owner-cli')
    await expect(field('owner/cli', '源名称')).toHaveValue('cli')
    await expect(dialog.getByText('已有同仓库的源')).toHaveCount(1)

    await dialog.getByRole('button', { name: '移除 extra/tool' }).click()
    await expect(dialog.getByRole('heading', { name: /预览 3 个同步源/ })).toBeVisible()
    await expect(dialog.getByLabel('GitHub 仓库')).not.toHaveValue(/extra\/tool/)
    await expect(dialog.getByLabel('GitHub 仓库')).toHaveValue(/not-a-repo/)
    await expect(dialog.locator('code.cron')).toHaveText(['0 3 * * *', '10 3 * * *', '20 3 * * *'])

    // Conflicts block the save before anything is written.
    await field('owner/cli', '唯一标识').fill('cli')
    await expect(dialog.getByText('与其他行重复', { exact: true })).toHaveCount(2)
    await field('owner/cli', '目的目录').fill('github')
    await expect(dialog.getByText('与已有同步源重复或嵌套')).toBeVisible()
    await dialog.getByRole('button', { name: '保存并写回 YAML' }).click()
    await expect(dialog.getByRole('alert')).toContainText('请先修正预览表中标红的项')
    await field('owner/cli', '唯一标识').fill('owner-cli')
    await field('owner/cli', '目的目录').fill('tools/owner-cli')

    // Only untouched dest values follow the prefix.
    await dialog.getByLabel('目的目录前缀').fill('gh')
    await expect(field('cli/cli', '目的目录')).toHaveValue('gh/cli')
    await expect(field('owner/cli', '目的目录')).toHaveValue('tools/owner-cli')
    await field('cli/cli', '附件 glob').fill('*linux*amd64*, *checksums*')
    await field('cli/cli', '源名称').fill('GitHub CLI')

    await dialog.getByRole('button', { name: '保存并写回 YAML' }).click()
    await expect(dialog).toHaveCount(0)
    await expect(page.getByRole('row').filter({ hasText: 'GitHub CLI' })).toBeVisible()

    const saved = (await (await page.request.get(`${base}/api/config`)).json()) as {
      config: {
        sources: {
          id: string
          name: string
          dest: string
          enabled: boolean
          github_release: { repository: string; asset_patterns: string[] }
          schedule: { enabled: boolean; cron: string }
        }[]
      }
    }
    const added = saved.config.sources.slice(1).map((source) => ({
      id: source.id,
      name: source.name,
      dest: source.dest,
      enabled: source.enabled,
      repository: source.github_release.repository,
      patterns: source.github_release.asset_patterns,
      schedule: source.schedule,
    }))
    expect(added).toEqual([
      {
        id: 'openlistteam-openlist',
        name: 'OpenList',
        dest: 'gh/openlistteam-openlist',
        enabled: true,
        repository: 'OpenListTeam/OpenList',
        patterns: ['*.tar.gz'],
        schedule: expect.objectContaining({ enabled: true, cron: '0 3 * * *' }),
      },
      {
        id: 'cli',
        name: 'GitHub CLI',
        dest: 'gh/cli',
        enabled: true,
        repository: 'cli/cli',
        patterns: ['*linux*amd64*', '*checksums*'],
        schedule: expect.objectContaining({ enabled: true, cron: '10 3 * * *' }),
      },
      {
        id: 'owner-cli',
        name: 'cli',
        dest: 'tools/owner-cli',
        enabled: true,
        repository: 'owner/cli',
        patterns: ['*.tar.gz'],
        schedule: expect.objectContaining({ enabled: true, cron: '20 3 * * *' }),
      },
    ])
    expect(saved.config.sources[0]?.id).toBe('openlist')

    // Editing an existing source never offers the batch switch.
    await page.getByRole('button', { name: '编辑 GitHub CLI' }).click()
    await expect(page.getByRole('dialog').getByRole('switch', { name: '批量添加' })).toHaveCount(0)
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
