import { test, expect } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { mkdtemp, writeFile, readFile, rm, mkdir } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'

async function freePort():Promise<number> {return new Promise((resolve,reject)=>{const server=net.createServer();server.on('error',reject);server.listen(0,'127.0.0.1',()=>{const port=(server.address() as net.AddressInfo).port;server.close(()=>resolve(port))})})}
async function stop(child:ChildProcess) {if(child.exitCode!==null)return;child.kill('SIGTERM');await Promise.race([new Promise(resolve=>child.once('exit',resolve)),new Promise(resolve=>setTimeout(()=>{child.kill('SIGKILL');resolve(null)},5000))])}

test('real configuration, rsync failover, publication, records and responsive UI',async({page,request},testInfo)=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message))
 const root=path.resolve(import.meta.dirname,'../..');const dir=await mkdtemp(path.join(tmpdir(),'mirrordock-e2e-'));const port=await freePort();const daemonPort=await freePort();const missingPort=await freePort();const base=`http://127.0.0.1:${port}`
 const upstream=path.join(dir,'upstream');await mkdir(upstream);await writeFile(path.join(upstream,'package.rpm'),'real package from isolated upstream');
 const rsync=process.env.MIRRORDOCK_TEST_RSYNC
 test.skip(!rsync,'Set MIRRORDOCK_TEST_RSYNC to a modern rsync executable')
 await writeFile(path.join(dir,'rsyncd.conf'),`use chroot = no\nuid =\ngid =\nmunge symlinks = no\n[repo]\npath = ${upstream}\nread only = yes\n`)
 const daemon=spawn(rsync!,['--daemon','--no-detach','--address=127.0.0.1',`--port=${daemonPort}`,`--config=${path.join(dir,'rsyncd.conf')}`],{stdio:'ignore'})
 const configFile=path.join(dir,'config.yaml');await writeFile(configFile,`# e2e comment\nlisten: 127.0.0.1\nport: ${port}\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: mirror-admin\n  password: strong local password 123\nsettings:\n  task_timeout: 30s\n  rsync:\n    connect_timeout: 2s\n    io_timeout: 2s\nsources: []\n`)
 let output='';const app=spawn(process.env.MIRRORDOCK_TEST_BINARY ?? path.join(root,'bin','mirrordock'),['--config',configFile],{env:{...process.env,TZ:'Asia/Shanghai',PATH:`${path.dirname(rsync!)}${path.delimiter}${process.env.PATH}`},stdio:['ignore','ignore','pipe']});app.stderr!.on('data',chunk=>{output+=String(chunk)})
 try {
  await expect.poll(async()=>{try{return (await request.get(base+'/api/auth/status')).status()}catch{return 0}}).toBe(200)
  expect(output).not.toContain('初始化码');expect(output).not.toContain('strong local password 123')
  await page.goto(base+'/admin/');await expect(page.getByLabel('初始化码')).toHaveCount(0)
  await page.getByLabel('用户名',{exact:true}).fill('wrong-user');await page.getByLabel('管理员密码').fill('strong local password 123');await page.getByRole('button',{name:'登录管理台'}).click();await expect(page.getByRole('alert')).toContainText('用户名或密码错误')
  await page.getByLabel('用户名',{exact:true}).fill('mirror-admin');await page.getByRole('button',{name:'登录管理台'}).click()
  await expect(page.getByRole('heading',{name:'同步源.'})).toBeVisible();await expect(page.locator('.account strong')).toHaveText('mirror-admin');await expect(page.locator('.topbar')).toHaveCount(0);await expect(page.getByText('LOCAL MIRROR',{exact:true})).toHaveCount(0)
  await page.getByRole('button',{name:'新增同步源'}).first().click();await expect(page.locator('select')).toHaveCount(0)
  const engine=page.getByRole('combobox',{name:'同步方式',exact:true})
  await engine.click();await expect(page.getByRole('listbox')).toBeVisible();await engine.press('Escape');await expect(page.getByRole('listbox')).toHaveCount(0);await expect(page.getByRole('dialog')).toBeVisible()
  await engine.click();await page.getByRole('option',{name:'GitHub Release',exact:true}).click();await expect(page.getByLabel('GitHub 仓库')).toBeVisible()
  await engine.press('Home');await engine.press('Enter');await expect(engine).toHaveText('rsync');await expect(page.getByLabel('GitHub 仓库')).toHaveCount(0)
  await page.getByLabel('源名称').fill('隔离测试源');await page.getByLabel('唯一标识').fill('isolated');await page.getByLabel('目的目录').fill('docker-ce');await page.getByPlaceholder('rsync://mirror.example/repo/').fill(`rsync://127.0.0.1:${missingPort}/repo/`);await page.getByRole('button',{name:'添加备用来源'}).click();await page.getByPlaceholder('rsync://mirror.example/repo/').nth(1).fill(`rsync://127.0.0.1:${daemonPort}/repo/`);await page.getByRole('button',{name:'保存并写回 YAML'}).click()
  await expect(page.getByRole('dialog')).toHaveCount(0);const row=page.getByRole('row').filter({hasText:'隔离测试源'});await expect(row).toBeVisible();await row.getByRole('button',{name:'同步',exact:true}).click()
  await page.getByRole('link',{name:'批次记录'}).click();await expect(page.locator('.run-row').first().getByText('成功',{exact:true})).toBeVisible({timeout:20000});await expect(page.locator('.run-row').first()).toContainText('已尝试 2 次')
  for(const [name,option] of [['时间范围','最近 24 小时'],['同步源','隔离测试源'],['运行来源','手动触发']]) {
   await page.getByRole('combobox',{name,exact:true}).click();await page.getByRole('option',{name:option,exact:true}).click();await expect(page.locator('.run-row')).toHaveCount(1)
  }
  const origin=page.getByRole('combobox',{name:'运行来源',exact:true});await origin.click();await page.getByRole('option',{name:'自动计划',exact:true}).click();await expect(page.locator('.run-row')).toHaveCount(0);await origin.press('Home');await origin.press('Enter')
  const status=page.getByRole('combobox',{name:'批次状态',exact:true})
  await status.click();await page.getByRole('option',{name:'失败',exact:true}).click();await expect(page.locator('.run-row')).toHaveCount(0)
  await status.press('Home');await status.press('Enter');await expect(page.locator('.run-row')).toHaveCount(1)
  await status.click();await page.waitForTimeout(1200);await expect(page.getByRole('listbox')).toBeVisible();await page.screenshot({path:testInfo.outputPath('custom-dropdown.png'),fullPage:true})
  await page.getByRole('heading',{name:'批次记录.'}).click();await expect(page.getByRole('listbox')).toHaveCount(0)
  const download=await request.get(base+'/docker-ce/package.rpm');expect(download.status()).toBe(200);expect(await download.text()).toBe('real package from isolated upstream')
  const partial=await request.get(base+'/docker-ce/package.rpm',{headers:{Range:'bytes=0-3'}});expect(partial.status()).toBe(206);expect(await partial.text()).toBe('real')
  await page.screenshot({path:testInfo.outputPath('batches.png'),fullPage:true})
  await page.getByRole('button',{name:'查看运行详情'}).first().click();await expect(page.getByRole('dialog')).toContainText('UPSTREAM_ERROR')
  const attempt=page.getByRole('combobox',{name:'选择日志尝试'})
  await expect(attempt).toHaveText('尝试 #2');await attempt.click();await page.getByRole('option',{name:'尝试 #1',exact:true}).click();await expect(attempt).toHaveText('尝试 #1');await expect(page.locator('.log-view')).toContainText('Connection refused')
  await attempt.press('End');await attempt.press('Enter');await expect(attempt).toHaveText('尝试 #2');await expect(page.locator('.log-view')).toContainText('Total transferred file size: 35 bytes')
  await page.getByRole('button',{name:'关闭',exact:true}).click()
  await page.getByRole('link',{name:'运行记录'}).click();await expect(page.locator('.table-scroll table')).toContainText('隔离测试源');await page.getByRole('combobox',{name:'按同步源筛选'}).click();await page.getByRole('option',{name:'隔离测试源',exact:true}).click();await page.getByRole('combobox',{name:'按时间筛选'}).click();await page.getByRole('option',{name:'最近 24 小时'}).click();await expect(page.locator('.table-scroll table')).toContainText('隔离测试源');await page.screenshot({path:testInfo.outputPath('records.png'),fullPage:true})
  await page.getByRole('link',{name:'系统设置'}).click();await page.getByLabel('rsync 传输限速').fill('12.5');await page.getByLabel('GitHub 代理 · proxy').fill('socks5://user:e2e-proxy-password@127.0.0.1:1080');await page.getByRole('button',{name:'保存设置'}).click();await expect.poll(async()=>await readFile(configFile,'utf8')).toContain('limit_mb: 12.5');expect(await readFile(configFile,'utf8')).toContain('# e2e comment');expect(await readFile(configFile,'utf8')).toContain('socks5://user:e2e-proxy-password@127.0.0.1:1080');await expect(page.getByLabel('GitHub 代理 · proxy')).toHaveValue('');const proxyView=await (await page.request.get(base+'/api/config')).json();expect(proxyView.proxy_configured).toBe(true);expect(proxyView.config.settings.github.proxy).toBe('');expect(proxyView.config.auth.username).toBe('mirror-admin');expect(proxyView.config.auth).not.toHaveProperty('password');expect(await readFile(configFile,'utf8')).toContain('password: strong local password 123');expect(JSON.stringify(proxyView)).not.toContain('strong local password 123');await page.getByLabel('清空代理并直连').check();await page.getByRole('button',{name:'保存设置'}).click();await expect.poll(async()=>((await (await page.request.get(base+'/api/config')).json()).proxy_configured)).toBe(false)
  await page.getByRole('link',{name:'同步源',exact:true}).click();await page.getByRole('button',{name:'编辑 隔离测试源'}).click();await expect(page.getByRole('dialog')).toContainText('编辑同步源');await page.getByRole('button',{name:'取消',exact:true}).click()
  for(const width of [1440,1024,736,375]) {await page.setViewportSize({width,height:1000});for(const name of ['sources','batches','records','schedules','settings']) {await page.goto(base+'/admin/'+name);await expect(page.locator('.page-heading')).toBeVisible();await expect(page.locator('select')).toHaveCount(0);await expect(page.locator('.topbar,.crumb,.topbar-right')).toHaveCount(0);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBeTruthy()}}
  await page.getByRole('button',{name:'打开导航'}).click();await expect(page.locator('.sidebar')).toHaveClass(/open/);await page.getByRole('link',{name:'同步源',exact:true}).click();await expect(page.locator('.sidebar')).not.toHaveClass(/open/);await expect.poll(async()=>{const box=await page.locator('.sidebar').boundingBox();return box?box.x+box.width:Infinity}).toBeLessThanOrEqual(0);await expect(page.locator('.page-heading')).toContainText('同步源');await page.screenshot({path:testInfo.outputPath('mobile-sources.png'),fullPage:true})
  await page.goto(base+'/admin/batches');await page.getByRole('combobox',{name:'批次状态',exact:true}).click()
  const bounds=await page.getByRole('listbox').boundingBox();expect(bounds).toBeTruthy();expect(bounds!.x).toBeGreaterThanOrEqual(0);expect(bounds!.x+bounds!.width).toBeLessThanOrEqual(375);expect(bounds!.y+bounds!.height).toBeLessThanOrEqual(1000)
  await page.screenshot({path:testInfo.outputPath('mobile-dropdown.png'),fullPage:true});await page.getByRole('combobox',{name:'批次状态',exact:true}).press('Tab');await expect(page.getByRole('listbox')).toHaveCount(0)
  await page.setViewportSize({width:1440,height:1000});await page.goto(base+'/admin/sources')
  const filter=page.getByRole('combobox',{name:'筛选同步方式'})
  await filter.click();await page.getByRole('option',{name:'GitHub Release',exact:true}).click();await expect(page.locator('.source-name')).toHaveCount(0)
  await filter.press('Home');await filter.press('ArrowDown');await filter.press('Enter');await expect(page.locator('.source-name')).toHaveCount(1)
  const fonts=await page.evaluate(()=>({family:getComputedStyle(document.body).fontFamily,heading:getComputedStyle(document.querySelector('h1')!).fontWeight,button:getComputedStyle(document.querySelector('.heading-actions .button')!).fontWeight}))
  expect(fonts.family).not.toContain('Avenir');expect(fonts.heading).toBe('700');expect(fonts.button).toBe('500')
  const icon=await request.get(base+'/favicon.ico?v=2');const adminIcon=await request.get(base+'/admin/favicon.ico?v=2');expect(icon.status()).toBe(200);expect(adminIcon.status()).toBe(200);const iconBytes=await icon.body();expect(iconBytes.equals(await adminIcon.body())).toBeTruthy();expect([...iconBytes.subarray(0,6)]).toEqual([0,0,1,0,6,0]);expect(await page.locator('link[rel="icon"]').getAttribute('href')).toBe('/admin/favicon.ico?v=2')
  await page.screenshot({path:testInfo.outputPath('sources.png'),fullPage:true});expect(errors).toEqual([])
  await page.getByRole('button',{name:'退出登录'}).click();await expect(page.getByLabel('用户名',{exact:true})).toBeVisible();await page.getByLabel('用户名',{exact:true}).fill('mirror-admin');await page.getByLabel('管理员密码').fill('strong local password 123');await page.getByRole('button',{name:'登录管理台'}).click();await expect(page.locator('.account strong')).toHaveText('mirror-admin');expect(output).not.toContain('strong local password 123');expect(errors).toEqual([])
 } finally {await stop(app);await stop(daemon);await rm(dir,{recursive:true,force:true})}
})
