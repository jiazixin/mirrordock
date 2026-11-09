import { test, expect, type Locator } from '@playwright/test'
import { spawn } from 'node:child_process'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'
import { defined } from './support'

async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = net.createServer()
    server.on('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const value = (server.address() as net.AddressInfo).port
      server.close(() => {
        resolve(value)
      })
    })
  })
}

async function box(locator: Locator) {
  return defined(await locator.boundingBox(), 'bounding box')
}

/** Box of the closest <label> wrapping the control, read from layout. */
async function labelBox(control: Locator) {
  return control.evaluate((element) => {
    const label = element.closest('label')
    if (!label) throw new Error('control is not wrapped in a label')
    const rect = label.getBoundingClientRect()
    return { x: rect.x, y: rect.y, width: rect.width, height: rect.height }
  })
}

test('field labels and option rows only react to clicks on the control itself', async ({
  page,
  request,
}) => {
  const dir = await mkdtemp(path.join(tmpdir(), 'mirrordock-click-targets-'))
  const port = await freePort()
  const base = `http://127.0.0.1:${port}`
  const config = path.join(dir, 'config.yaml')
  await writeFile(
    config,
    `listen: 127.0.0.1
port: ${port}
path: ./state
mirror_root: ./mirror
auth:
  username: admin
  password: click-target-fixture-password
sources: []
`,
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
    await page.goto(`${base}/admin/sources`)
    await page.getByLabel('用户名', { exact: true }).fill('admin')
    await page.getByLabel('管理员密码').fill('click-target-fixture-password')
    await page.getByRole('button', { name: '登录管理台' }).click()
    await expect(page.getByRole('heading', { name: '同步源', exact: true })).toBeVisible()
    await page.getByRole('button', { name: '新增同步源' }).first().click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    // Geometry is read once, so the dialog must not be mid-transition.
    await page.evaluate(() =>
      Promise.all(
        document
          .getAnimations()
          .filter((animation) => animation.effect?.getComputedTiming().endTime !== Infinity)
          .map((animation) => animation.finished),
      ),
    )

    // Select field: title text, the gap and the grid-stretched space below the box stay inert.
    const trigger = dialog.getByRole('combobox', { name: '同步方式', exact: true })
    const triggerBox = await box(trigger)
    const typeLabel = await labelBox(trigger)
    expect(typeLabel.height).toBeGreaterThan(triggerBox.height)
    for (const [x, y] of [
      [typeLabel.x + 10, typeLabel.y + 8],
      [typeLabel.x + typeLabel.width - 20, typeLabel.y + 8],
      [triggerBox.x + 40, triggerBox.y - 4],
      [triggerBox.x + 40, triggerBox.y + triggerBox.height + 3],
    ] as const) {
      await page.mouse.click(x, y)
      await expect(trigger).toHaveAttribute('aria-expanded', 'false')
    }
    await trigger.click()
    await expect(trigger).toHaveAttribute('aria-expanded', 'true')
    await page.keyboard.press('Escape')
    await expect(trigger).toHaveAttribute('aria-expanded', 'false')

    // Text field: clicking its title does not move focus into the input.
    const name = dialog.getByRole('textbox', { name: '源名称' })
    const nameLabel = await labelBox(name)
    await page.mouse.click(nameLabel.x + 10, nameLabel.y + 8)
    await expect(name).not.toBeFocused()
    await name.click()
    await expect(name).toBeFocused()

    // Switch rows: padding, the switch/text gap and the blank under the switch stay inert.
    const enabled = dialog.getByRole('switch', { name: '启用此同步源' })
    const switchBox = await box(enabled)
    const title = dialog.getByText('启用此同步源', { exact: true })
    const titleBox = await box(title)
    await expect(enabled).toBeChecked()
    for (const [x, y] of [
      [switchBox.x + switchBox.width / 2, switchBox.y - 3],
      [switchBox.x + switchBox.width / 2, switchBox.y + switchBox.height + 10],
      [switchBox.x + switchBox.width + 6, switchBox.y + switchBox.height / 2],
      [titleBox.x + titleBox.width + 4, titleBox.y + titleBox.height / 2],
    ] as const) {
      await page.mouse.click(x, y)
      await expect(enabled).toBeChecked()
    }
    await enabled.click()
    await expect(enabled).not.toBeChecked()
    await title.click()
    await expect(enabled).toBeChecked()
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
