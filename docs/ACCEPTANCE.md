# 验收记录

## 公开目录标题与表头对齐微调（2026-10-17）

范围：`internal/mirrors/listing.{html,css}`、`internal/mirrors/listing_test.go`。

- 子目录标题不再加绿色句点（目录名常含 `.`，如 `v4.2.7`，句点有歧义），也不再显示与面包屑重复的完整路径副标题；首页“镜像目录.”与说明文字保持不变。
- 除名称列外，表头与单元格改为居中对齐（文件列表：类型 / 大小 / 修改时间；镜像列表：状态 / 大小 / 上次同步 / 下次同步）。排序箭头为绝对定位，不参与居中计算。同日首版只把类型表头左移 8px、仍为左对齐，用户指出未居中后改为本方案并撤销该偏移。640px 以下卡片行恢复左对齐，排序胶囊不变。
- 单元测试：首页仍断言句点；子目录断言无句点、无 `page-subtitle`。

| 检查 | 结果 |
| --- | --- |
| `go test -race -count=1 ./internal/mirrors`、`go vet ./internal/mirrors`、`git diff --check` | 通过 |
| E2E `public-directory.spec.ts`（`MIRRORDOCK_TEST_RSYNC=.cache/rsync/rsync-3.5.1/rsync`，scratchpad 临时二进制） | 2/2 通过 |
| 浏览器实测（scratchpad 静态渲染的 `/github/openlist/` 文件列表与 `/github/` 镜像列表示例页） | 1280 宽：表头文字（不含箭头）中心与每行内容（标签 / 状态胶囊 / 两行时间按整体）中心横坐标一致：类型 723、大小 878.7、修改时间 1055.2；状态 557、大小 712.7、上次同步 878.7、下次同步 1065.6。375 宽单元格为左对齐，无横向滚动 |
| open-code-review | 未执行外部 LLM；`ocr delegate preview/rule` 选出可审文件（首版 listing.css、listing.html；居中修正 listing.css）由当前会话审查，排除的 listing_test.go（default_path）已人工核对。未发现正确性、安全性或性能问题 |
| 未执行 | Web 格式检查与构建（本次无 `web/` 源码变化）、其他 E2E |

## 管理台切换过渡动画（2026-10-17）

范围：`web/src/app/router.ts`、`web/src/layouts/Shell.vue`、`web/src/styles/global.css`。用户要求为管理台点击切换添加一套线性过渡动画。

- 侧栏切换页面：`RouterView` 外包 `Transition`（`out-in`），旧页 0.12s 线性淡出，新页 0.2s 线性淡入并上移 6px。三个分析路由标记 `meta.view = 'analytics'` 共用同一个 key，切换分析类型时组件不重建（与原先行为一致），只由标签下划线表现切换。
- 标签：当前项下划线 0.2s 线性从 `scaleX(0)` 展开；`role="tabpanel"` 面板（系统设置）显示时 0.2s 线性淡入上移 4px，`v-show` 从 `display: none` 恢复时同样触发。侧栏链接与标签文字颜色、侧栏背景改为 0.2s 线性过渡。
- 全局 `prefers-reduced-motion: reduce` 重置仍会关闭以上动画；进入时的短暂 `transform` 不影响弹窗与下拉菜单（均 Teleport 到 `body`）。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` / `npm --prefix web run build`（含 `vue-tsc`） | 通过 |
| 全部 Web E2E（`MIRRORDOCK_TEST_RSYNC=.cache/rsync/rsync-3.5.1/rsync`，scratchpad 临时测试二进制） | 14 通过，0 失败，0 跳过。此后只删除了包裹层上一个未使用的 class，重新执行了格式检查和构建，E2E 未再重跑 |
| open-code-review | 外部 LLM 未执行：`ocr review` 返回 HTTP 401（3/3 文件失败）。改用 `ocr delegate preview`（3 个可审文件，无排除），由当前会话审查 3/3；确认无依赖页面直接子元素的选择器、无未 Teleport 的 `position: fixed` 元素受进入动画 `transform` 影响。修复 1 项：删除包裹层未使用的 `page-view` class |

未验证：未在浏览器中逐帧目测动画观感；Linux 发行版浏览器观感；Go 测试未运行（未改 Go 代码）。

## 公开镜像目录浅色改版（2026-10-17）

范围：`internal/mirrors/listing.{go,html,css,js}`、`internal/mirrors/listing_test.go`、`web/e2e/public-directory.spec.ts`，以及 README 中一句过时的深浅色说明。设计依据为用户确认的设计稿；用户要求只保留浅色。

- 页头改为与管理台一致的深绿方形 “M” Logo；首页标题为“镜像目录.”（句点 `aria-hidden`），子目录标题为当前目录名，副标题为完整路径。首页不再显示面包屑；子目录面包屑缩为标题上方小字，首项为“镜像目录”，自动换行，不再横向滚动（同时删除 `listing.js` 中滚动面包屑的代码）。
- 含镜像项目的列表新增浅绿汇总卡：镜像数、同步正常（成功）、需要关注（失败 / 超时 / 中断）、公开文件总量。数据由已渲染的行计算，不新增数据库查询；全部大小未知时显示“—”。
- 列表改为圆角卡片、灰底表头、60px 行高；图标放进浅色圆角方块（目录绿、软件包蓝、签名 / 校验和琥珀、其他灰）；状态改为浅色胶囊；类型改为灰色标签；上次 / 下次同步显示为“相对时间 + 月-日 时:分”两行。
- 640px 以下表头变为可横滑的排序胶囊，行变为两行卡片（名称 + 状态 / 类型；大小与时间），空值单元格隐藏。
- 删除 `prefers-color-scheme: dark` 样式，增加 `color-scheme` / `theme-color` 浅色声明。搜索、排序、无 JavaScript 链接、1000 项截断提示、CSP 脚本哈希与下载热路径未改。
- 测试调整：首页面包屑、“首页”文字与“当前位置”标签的断言改为新设计（首页无面包屑、首项“镜像目录”）；`expectHeaderLayout` 的“面板距标题不超过 140px”改为标题、汇总卡与工具栏之间固定间距（28 / 36px）；新增汇总计数、大小未知和文件列表不显示汇总卡的单元测试。

| 检查 | 结果 |
| --- | --- |
| `go vet ./...` | 通过 |
| `go test -race -count=1 ./internal/mirrors` | 通过 |
| `npm --prefix web run format:check` / `npm --prefix web run build` | 通过 |
| E2E `public-directory.spec.ts`（`MIRRORDOCK_TEST_RSYNC=.cache/rsync/rsync-3.5.1/rsync`，临时测试二进制） | 2/2 通过 |
| 浏览器实测（scratchpad 临时目录、127.0.0.1:48732 隔离实例，示例源未执行同步） | 1280 宽首页 / `/rocky/9/BaseOS/`、736 宽 `/github/`、375 与 320 宽首页和子目录：无横向滚动；搜索、按大小排序、条目计数正常，无控制台错误。发现并修复 2 处：`.site-wrap` 内边距覆盖了 `main` 顶部间距；手机端文件列表排序胶囊被桌面列宽挤压 |
| open-code-review | 外部 LLM 未执行：`ocr review` 0 token，4/4 文件失败（“check your LLM configuration and API key”）。改用 `ocr review --preview` 与 `ocr rules check` 的选择与规则，由当前会话审查 4/4 个可审文件；排除的 2 个测试文件（默认路径）已人工核对。未发现需修复问题 |

未验证：其他 E2E 规格未重跑（未涉及公开目录）；Linux 发行版浏览器观感；成功 / 同步中 / 失败等彩色状态胶囊只经单元与 E2E 断言，实测实例中的源均未同步，只看到“已发布”灰色胶囊。

## 管理台白绿浅色改版（2026-10-17）

范围：未提交工作区中 `web/` 下 17 个文件（含删除 `web/src/app/theme.ts`）。设计依据为用户确认的概念稿（沿用其旧项目 video-rename 的设计语言）；用户要求只保留浅色。

- 外壳：顶部横向导航改为白色侧栏（分组：工作台 / 任务记录 / 运行管理 / 分析 / 系统，带图标；当前项浅绿底深绿字），底部为实时推送状态、镜像目录链接、头像、用户名与退出按钮。900px 以下折为顶栏 + 下拉抽屉，沿用 `.primary-nav` / `open`、“打开导航”“退出登录”等既有名称。
- 主题：删除深色两套令牌与 `theme.ts` 主题切换（含 `localStorage` 写入）；`index.html` 只保留白色 `theme-color`。令牌改为白底、深绿品牌色 `#1d6a45`、浅绿摘要卡、蓝色表示运行中、红 / 琥珀表示异常；成功改为浅绿。Logo 改为深绿方块白色 “M”；`favicon.ico` 未改，仍是旧绿色图标。
- 通用组件：页面标题 40px 粗体，后接 `aria-hidden` 绿色句点（不影响可访问名称）；页头按钮改为浅色着色按钮（默认灰绿，主操作浅绿，清理日志琥珀）；状态标签改为带边框的浅色胶囊；表头灰底；行内图标按钮按动作着色（同步 / 继续为绿，暂停为琥珀，取消 / 删除为红）；摘要统计条、运行详情统计改为浅绿卡片；进度条加粗为圆角；开关卡片改为圆形滑块、选中为品牌绿；系统设置分区放入卡片。
- 概览：标题“概览.” + 刷新按钮；原大标题状态并入浅绿摘要卡（状态文字按全部正常 / 运行中 / 需处理着色，四项指标，下一次计划）；时间轴、进行中、需要处理改为带灰底表头的卡片；时间轴每行加同步方式图标；需要处理用彩色图标块代替圆点。数据加载、刷新和操作逻辑未改，只新增了手动刷新按钮（复用原有 30 秒定时刷新的函数）。
- 分析图表的“成功”系列由深灰改为绿色 `--chart-ok`。
- E2E 调整：`mirrordock.spec.ts` 中写死旧设计字重的断言改为新值（h1 `700`、页头按钮 `600`）。另外两项断言维持原样并改了样式来满足：同步方式标签保持单一颜色；批次筛选下拉宽度不超过 180px。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` | 通过 |
| `npm --prefix web run build`（含 `vue-tsc`） | 通过 |
| 全部 Web E2E（`MIRRORDOCK_TEST_RSYNC=.cache/rsync/rsync-3.5.1/rsync`，临时测试二进制） | 第一轮 12 通过、2 失败（同步方式标签颜色不统一、筛选下拉宽 190px > 180px），修复后两项单独复跑通过；最终全量结果见下一行 |
| 全部 Web E2E（最终构建） | 14 通过，0 失败，0 跳过。此后只删除了未使用的样式变体与 `--vio*` 令牌，改动后重新执行了格式检查和构建，E2E 未再重跑 |
| 浏览器实测（临时目录、127.0.0.1:18731 隔离实例、本机 127.0.0.1:18732 rsync daemon） | 1440 宽：登录、概览、同步源、批次、运行记录、计划、Rsync 分析、系统设置五个标签、同步源编辑弹窗、运行详情弹窗；375 宽：概览与导航抽屉，无横向滚动 |
| open-code-review | 外部 LLM 未执行：`ocr review` 0 token、14/14 文件失败（“check your LLM configuration and API key”）。改用 `ocr delegate preview` / `ocr delegate rule`，由当前会话按默认规则审查 14/14 个可审文件；排除 3 个（`e2e/mirrordock.spec.ts` 默认路径，已人工核对；`theme.ts` 删除，已确认无残留引用；`logo.svg` 扩展名不支持，已人工核对）。修复 3 项：hover 边框与行悬停色改用令牌，不再写死颜色值；`--t3` 加深到 `#6b746e`，使灰底胶囊上的小字对比度达标；删除未使用的紫 / 蓝着色变体与 `--vio*` 令牌 |

未验证：Linux 发行版浏览器下的观感；Go 单元测试未运行（本次未改 Go 代码）。旧版本用户浏览器里遗留的 `mirrordock.theme` 存储项不再读取，也未主动清除。

## 管理台视觉对齐与 undefined 刻度修复（2026-10-17）

范围：未提交工作区中 `web/src` 15 个文件与 `web/e2e` 6 个规格。

- 同步方式名称统一为 Rsync / Reposync / GitHub Release（`format.ts` 的 `sourceType()`），覆盖同步源、编辑器（含“Rsync 选项”“Cron 表达式”）、批次、运行记录、详情、计划、概览时间轴、登录页；分析页标签补齐空格（“Reposync 分析”“Release 分析”）。DNF 插件名“reposync”保持小写。
- 处理量趋势出现 `undefined`：空数据时最大值为 1 B，刻度 0.25/0.5/0.75 B 进入 `bytes()` 后单位下标为负。`bytes()` 对小于 1 的值返回 `0 B`；趋势图刻度改为整单位步长（字节按 1024 进制，耗时按 s/m/h）。
- 同步源“最近运行”“操作”列中轴对齐；操作统一为 30px 描边图标按钮（同步 / 编辑 / 删除，同底色 `--panel`），同步按钮 aria-label 为“同步 <名称>”。运行记录“详情”、计划管理“下次运行 / 自动计划 / 操作”同样居中，计划“立即同步”改为同款图标按钮。
- 下拉菜单：已选项用强调色与浅底标识，去掉对勾；菜单宽度随文字增长（不小于触发器、最大 420px），“50 条 / 页”不再截断。
- 视觉对齐：批次行操作改为描边按钮，与右上耗时右缘一致（实测均为 1379px）；手机宽度下操作按钮不再折成竖排；分析排行表首列与卡片标题对齐、次数左对齐、洞察图标与标题垂直居中；概览时间轴“现在”标签不再与相邻刻度重叠；运行详情刷新图标与内容右缘对齐。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` | 通过 |
| `npm --prefix web run build`（含 `vue-tsc`） | 通过 |
| 全部 Web E2E（`MIRRORDOCK_TEST_RSYNC=.cache/rsync/rsync-3.5.1/rsync`，临时测试二进制） | 14 通过，0 跳过；新增断言：空数据/有数据的处理量与耗时刻度文本、分页条数菜单不截断且标记已选、同步源两列中轴对齐与操作按钮等宽同色 |
| 浏览器实测（临时目录、127.0.0.1:8080 隔离实例、本机 127.0.0.1:8873 rsync daemon） | 1440 宽亮/暗色同步源、批次、记录、计划、概览、分析、设置、编辑器、运行详情；375 宽批次与同步源 |
| open-code-review | 外部 LLM 未执行：`ocr review` 返回 HTTP 401（invalid x-api-key，15/15 文件失败）。改用 `ocr review --preview` 与 `ocr rules check` 的选择与规则，由当前会话审查 15/15 个源码文件（E2E 规格被 OCR 默认排除，另行人工核对）；发现并修复 1 项：`global.css` 中重复的 `.center` 规则 |

未验证：Linux 发行版环境下的界面观感；Go 单元测试未运行（本次未改动 Go 代码）。

## 开关组件重设计（2026-10-17）

范围：`web/src/components/OptionCard.vue`（系统设置、通知设置、同步源编辑共用的开关卡片）。

- 问题：全局 `label { flex-direction: column }` 泄漏到组件根 `<label>`，组件只声明了 `display: flex`，开关被排成“文字居中、开关在下”的竖排，与控制室界面不符。
- 改动：显式改为横排（文案左、控制右）；新增等宽 `ON` / `OFF` 状态字（`aria-hidden`，状态仍由 `role="switch"` 表达）；轨道与滑块改为 3px / 2px 方角；选中色使用中性 `--btn` / `--btn-ink`，不再用强调色（强调色只标运行中工作）；悬停加 `--hover` 底色；禁用态避免与全局 `input:disabled` 透明度叠加；保留 forced-colors 与 reduced-motion 处理。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run build`（含 `vue-tsc`） | 通过 |
| `npm --prefix web run format:check` | 通过 |
| 浏览器实测（临时目录、127.0.0.1:18731 隔离实例） | 暗色 1440 宽 GitHub Release 页、亮色 375 宽通知设置页：横排、开/关两态与状态字正确，无横向滚动 |
| E2E `preflight.spec.ts`、`notifications.spec.ts` | 7 通过 |
| E2E `mirrordock.spec.ts` | 跳过：本机无现代 rsync，未执行 |
| open-code-review | 未完成：`ocr review` 外部 LLM 返回 HTTP 401（全部 1 个文件失败）；随后本机找不到 `ocr` 命令，delegate preview/rule 也未能执行。改由当前会话按 bug / 可访问性 / 主题 / 全局样式冲突清单手工审查该 1 个文件（覆盖 1/1），未发现需修复问题 |

## Linux 实机验收：AlmaLinux 10.2（2026-10-16）

范围：提交 `a4e0245` 的源码（`git archive` + `go mod vendor` + 本地构建的 `web/dist`），在用户提供的测试虚拟机 `test-mirrordock` 上运行：AlmaLinux 10.2、内核 6.12.0-211.22.1.el10_2.x86_64、20 核、3.5 GiB 内存，根分区 XFS（`reflink=1`），SELinux Permissive，系统 rsync 3.4.4、DNF 4.20。Go 1.27.2 放在 `~/mirrordock-test`，未写入系统路径；为 race 与 strace 安装了 `gcc`、`strace`（用户注释掉 dnf.conf 的 exclude 后安装 gcc）。测试实例只监听 127.0.0.1:18080，上游为本机 127.0.0.1:18873 的 rsync daemon 与随机数据夹具；未访问真实发行版镜像，未改动用户配置或生产数据。

### 已执行检查

| 项目 | 结果 |
| --- | --- |
| `go vet ./...` | 通过 |
| `go test -count=1 ./...`（`MIRRORDOCK_TEST_RSYNC=/usr/bin/rsync`） | 9 个包全部通过，161 个测试，0 跳过 |
| `go test -race -count=1 ./...`（同上，CGO_ENABLED=1） | 9 个包全部通过，161 个测试，0 跳过，无 data race |
| 环境检查 API | reflink 实测“支持”；`statfs` 总量/可用量与 `df -B1` 一致（494.3 / 481.3 GiB），余量 4.94 GiB、准入需 5.44 GiB |
| 首次 rsync 同步与下载 | 成功；256 MiB 文件经公开 HTTP 下载与上游逐字节一致 |
| 空占位 dest | 启动不报错，首次发布后替换为发布链接 |
| reflink 共享块 | 仅修改一个小文件后发布新代：文件系统已用空间增加约 260 KB；新代中 256 MiB 文件的 extent 标记为 shared（FIEMAP flags 0x2001），内容与旧代一致 |
| HTTP/1.1 sendfile | `strace` 观测到 384 次 `sendfile(…)`，累计约 268 MB，即整个 256 MiB 文件经 sendfile 发出 |
| 同步进程优先级 | rsync 父子进程 nice=10、ionice best-effort 7，同一进程组 |
| 暂停 / 继续 | 暂停后 rsync 进程组全部退出，`.rsync-partial` 保留；继续沿用同一 work 目录 |
| 正常停机（SIGTERM） | rsync 无残留；数据库记为 interrupted / SERVICE_INTERRUPTED |
| 停机期间改 YAML 后重启 | 改 dest 的中断任务自动 cancelled（SOURCE_CHANGED），work 已清理；dest 迁移后新路径 200、旧路径 404 |
| dest 被普通文件占用 | 服务照常启动；环境检查与重载均报告该源问题（重载返回 200 + 警告），用户文件未被改动 |
| 配置重载 | 修改 `limit_mb` 后重载生效（rsync 以 `--bwlimit=1953` 运行） |
| GitHub Release 直连 | jqlang/jq 最新 Release（jq-1.8.2）两个附件同步成功，公开下载的 SHA-256 与 GitHub digest 一致 |
| GitHub Release 经 socks5h 代理 | 代理 `proxy.jzxk.cc:2002` 连通；其出口 IP 的匿名 API 配额已被耗尽（GitHub 返回 403 rate limit），任务正确归类为 GITHUB_RATE_LIMIT 并按重置时间安排重试，随后手动取消。经该代理的完整下载未验证 |
| systemd（临时单元：UMask 0077、NoNewPrivileges、ProtectSystem=strict、ProtectHome=read-only、PrivateTmp、LimitNOFILE=8192、Restart=on-failure） | 以普通用户运行；同步中对主进程 SIGKILL，systemd 清理整个 cgroup 后重启，任务自动恢复并成功、内容一致；正常停止退出成功 |

### 发现的问题（已在下一节修复并复测）

**中：服务进程被 SIGKILL（OOM、崩溃、`kill -9`）时，rsync 派生的接收子进程不会退出。** `Pdeathsig` 只作用于直接子进程；rsync 自己 fork 的接收进程被 init 接管后继续向 work 目录写入（实测在服务被杀后，partial 文件从 4 MB 增长到 68 MB），并一直闲置存活（观察超过 8 分钟）。重启后恢复的任务把同一 work 目录改名成发布代，残留进程的 cwd 随之落在**已发布的不可变代**内。本次残留进程恰好已写完，发布内容正确，但不能保证每次都如此。

- systemd（默认 `KillMode=control-group`）与容器部署可兜底：实测 systemd 在重启前杀掉整个 cgroup，但残留的 rsync 不响应 SIGTERM，要等满 `TimeoutStopSec=45` 后才 SIGKILL，崩溃后约 50 秒才恢复服务。
- 裸进程（nohup/setsid、无 cgroup 清理的进程管理器）部署没有兜底。
- 修复与复测见下一节“残留执行器进程修复与复测”。

### 残留执行器进程修复与复测（2026-10-17）

改动：
- `internal/sync/strays*.go` 新增 `TerminateStrays`。Linux 扫描 `/proc`，Darwin 用 `ps`，只看**本用户**进程中**参数**引用 `<mirror_root>/.mirrordock/work/` 的进程；先 SIGKILL，再轮询直到消失，最多 10 秒。不按工作目录匹配，以免误杀停留在 work 目录的管理员 shell 或服务发布代的 rsync daemon；其他用户的进程不纳入。
- `app.OpenConfig` 在持有实例锁、构建发布管理器之后、恢复任务之前调用它：结束时记 WARN 与数量，未能结束记 ERROR，启动不因此中止。
- `command()` 在执行器退出后再对其进程组发送一次 SIGKILL，清理已关闭标准输出、脱离父进程的同组子进程。

测试：新增 `TestTerminateStraysStopsOnlyWorkProcesses`。它断言参数引用 work 的进程被结束，仅工作目录在 work 内的进程和在发布代内服务的进程保留，第二次扫描为空。新增 `TestCommandStopsDescendantsAfterExit`：去掉进程组清理时实测失败，恢复后通过。

| 检查 | 结果 |
| --- | --- |
| 本机 `go vet ./...`、`gofmt -l` | 通过 |
| 本机 `go test -race -count=1 ./...`（rsync 3.5.1） | 全部包通过 |
| 虚拟机 `go vet ./...` | 通过 |
| 虚拟机 `go test -race -count=1 ./...`（`/usr/bin/rsync` 3.4.4） | 9 个包全部通过，164 个测试，0 跳过，无 data race；两个新测试在 Linux 上通过 |
| 虚拟机裸进程复现（setsid 启动，限速同步 96 MiB 时 SIGKILL 服务） | 2 秒后仍有 1 个残留 rsync（父进程为 1），复现成功。重启 324 ms 内就绪，日志 `terminated leftover executor processes count=1`；之后只剩新尝试的 rsync（父进程为新服务），任务第 2 次尝试成功、内容一致；没有任何进程的 cwd 落在 `.mirrordock/` 内 |
| 虚拟机 systemd 回归（同上加固参数，普通用户运行） | 同步中对主进程 SIGKILL 后约 37 秒自动重启，任务恢复成功、内容一致；测试目录无非 `k` 属主文件 |

外部 `ocr review` 7 个请求全部失败，未产生结果，不计为通过。改用 `ocr delegate preview`（5 个可审查文件：`app.go`、`process.go`、`strays.go`、`strays_linux.go`、`strays_darwin.go`；测试与 ACCEPTANCE 为默认排除）和 Go 规则组，由当前会话逐文件审查，覆盖 5/5。审查修复：

- 中：最初的实现也匹配工作目录在 work 内的进程，可能误杀管理员 shell，且对其他用户进程反复 EPERM、拖满 10 秒——改为只匹配本用户进程的参数。

遗留观察：systemd 部署下崩溃恢复仍取决于 systemd 自身的停止阶段（残留 rsync 不响应 TERM 时最多等 `TimeoutStopSec`），本修复不改变这一点；如需更快恢复，可在部署时酌情调小 `TimeoutStopSec`，但要保留正常停机所需的约 10 秒进程组退出时间。

### 未验证项

- 生产规模（多 TB、百万级文件）、长期运行、真实上游镜像、物理网卡吞吐、HTTPS/HTTP2 下的下载路径。
- 经 socks5 代理的 GitHub 完整下载（受代理出口 IP 的匿名配额限制；需 Token 或未受限出口）。
- Reposync / DNF4 与 GPG 校验本轮未在该虚拟机上重跑。
- SELinux Enforcing、真实断电与磁盘故障、Docker 镜像构建。
- 版本号：从 `git archive` 构建，无 `.git`，`--version` 显示 `dev`，属预期。

### 测试过程中的操作失误

为 systemd 测试编写的脚本里，自定义 shell 函数 `id` 覆盖了系统命令，导致第一次 `systemd-run --uid=$(id -u)` 以 root 启动服务约 1 分钟。该实例未执行任何同步任务，只在启动时以 root 身份重建了 3 个 dest 链接；已停止服务并把测试目录属主改回 `k`（复核 0 个非 `k` 文件），随后以普通用户重跑全部 systemd 用例。影响仅限测试目录。

### 清理

两轮测试后均已停止测试服务与 rsync daemon，删除临时单元与 `~/mirrordock-test/run` 测试数据。保留 `~/mirrordock-test` 下的 Go 工具链、源码与测试日志（约 1.3 GB）以便复测；`gcc`、`strace` 保留在系统中。

## 全项目逻辑审查问题 1–8 修复（2026-10-16）

范围：工作区未提交改动——`internal/runs/runs.go`、`internal/mirrors/mirrors.go`、`internal/app/{app,auth}.go`、`web/src/app/{state,types}.ts`、`web/src/features/settings/{Settings,EnvironmentChecks}.vue`，以及对应测试、README 与 DESIGN。下载热路径（`http.go`、`roots.go`）未改动。

| 编号 | 问题 | 处理 |
| --- | --- | --- |
| 1 | 启动恢复与 worker 出队不核对源配置：重启后已删除源的排队任务仍会执行并发布路由（已用临时测试复现） | 恢复、出队、“继续”统一核对当前 YAML：源删除或类型/dest 变化的任务自动 cancelled（`SOURCE_REMOVED`/`SOURCE_CHANGED`），禁用源的排队任务转 interrupted（`SOURCE_DISABLED`），启用后可继续；源被占用时配置保存同时拒绝改类型 |
| 2 | Reconcile 全有或全无：预先创建的空 dest 目录导致整个服务无法启动（已复现） | 逐源执行，单源失败记为 `SourceError` 并继续，启动不再因单源失败中止；失败列表进入程序日志与 `/api/system/checks` 的 `publication_issues`，环境检查新增“发布目录接入”行；空占位目录在首次发布时替换为链接，非空目录仍拒绝 |
| 3 | Rsync 多来源只看最后一个来源的错误决定是否重试 | 一轮内任一来源可重试即重试整轮；全部为永久来源错误仍直接失败 |
| 4 | 同步完成后发布阶段因低空间失败，终态清理丢弃整轮 work | Publish 不再检查余量；5 秒容量检查不取消已进入 publish 阶段的任务 |
| 5 | 同 ID 改类型时旧发布代被直接改标为新类型 | 保留旧发布的类型与路由，Prepare 不克隆不同类型的发布代，下次同步从空工作树开始 |
| 6 | 容量探测失败把排队任务直接变成 failed | 保持 queued/waiting_space，记录错误事件，恢复后自动执行 |
| 7 | 登录限流桶满 4096 后拒绝全部新 IP | IPv6 按 /64 分桶；满额淘汰最早窗口 |
| 8 | YAML 已保存但接入失败时返回 400，前端保留旧 revision 导致下次 409 | 返回 200、新 revision 与 `warning`；保存/重载均提示警告并采用新配置 |

### 已执行检查

| 命令 | 结果 |
| --- | --- |
| `go vet ./...`、`gofmt -l` | 通过，无输出 |
| `go test -race -count=1 ./...`（`MIRRORDOCK_TEST_RSYNC` 指向 `.cache/rsync/rsync-3.5.1`） | 全部包通过 |
| `npm --prefix web run format:check` | 通过 |
| `npm --prefix web run build`（含 `vue-tsc`） | 通过 |
| `npm --prefix web run test:e2e`（本轮构建的临时二进制，真实 rsync 3.5.1） | 首轮 13/14（原因见下文），修正后完整重跑 **14/14 通过**，约 1.8 分钟 |

新增测试：`internal/runs/conflict_test.go`（恢复取消/禁用中断/重新启用后继续、出队拦截禁用源与拒绝改类型、整轮重试与永久失败、容量探测失败保持排队）、`internal/mirrors/issues_test.go`（单源失败隔离与空占位替换、改类型不克隆旧布局、低空间仍可发布）、`internal/app/apply_test.go`（部分应用返回新 revision、警告与 `publication_issues`）、`internal/app/auth_test.go`（/64 分桶与满额淘汰）。

调整的既有测试：`TestImportDatabaseFailureRestoresOriginalDirectory` 改为断言启动成功、问题上报、原目录保留，并在之后的 Reconcile 中导入成功；`TestAliasFailureCannotPublish` 的占用目录改为含用户文件（空目录现在按设计会被替换），并断言用户文件不变；`TestRecoveryPagesAndPreservesPauseBudgetAndFailure` 的 105 条分页夹具改为指向已配置的源，类型被改的中断任务期望改为 cancelled。E2E 首轮 13/14：`pagination.spec.ts` 断言环境检查共 9 行，新增行后改为 10 行，并断言“发布目录接入”显示正常。

### OCR 与覆盖

`ocr review` 外部模型调用 11 个请求全部返回 HTTP 401，未产生审查结果，不计为通过。按 AGENTS 改用代理审查模式：`ocr delegate preview` 确定 8 个可审查文件，`ocr delegate rule` 给出 Go 与 TS/Vue 规则组，由当前会话逐文件审查。覆盖：8/8 已审查，0 跳过。排除项：README、DESIGN（不支持的扩展名），`*_test.go` 与 E2E（默认排除路径，仍随测试运行）。

审查发现并已修复：

- 低（正确性）：`clientIP` 忽略 `Prefix` 的错误时，异常地址会全部落入同一个 `invalid Prefix` 桶——改为出错时退回完整地址。
- 低（规范）：环境检查新增行使用嵌套三元表达式——提取为 `publicationStatus`。
- 低（死代码）：设置页 `reload` 仅转发调用——直接使用 `reloadConfiguration`，移除未用的导入。

### 未验证项

- Linux 目标环境、真实发行版与 XFS reflink 未参与本轮验证；全部检查在 macOS（APFS）本机执行。
- 自动取消依据的是重启时的 YAML：停机期间改动源的运行中任务会丢弃其 work 目录，这是有意的取舍，未在生产数据上演练。
- 只在 Chromium 上运行过 E2E。

## 管理台“控制室”界面改版（2026-10-16）

范围：分支 `ui-v2-console` 的工作区改动——`web/src` 全部页面与组件样式、新增概览页 `/overview`（登录后默认首页）与主题切换、顶部导航替代侧栏、Logo 与 favicon、`web/index.html`，以及公开目录 `internal/mirrors/listing.{html,css}` 的样式。API、配置协议、下载热路径与 Go 业务逻辑未改动。字体使用各平台系统字体（苹果为苹方），不加载远程字体。

### 已执行检查

| 命令 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` | 通过 |
| `npm --prefix web run build`（含 `vue-tsc`） | 通过 |
| `go vet ./...` | 通过 |
| `go test -race -count=1 ./...`（`MIRRORDOCK_TEST_RSYNC` 指向 `.cache/rsync/rsync-3.5.1`） | 全部包通过 |
| `npm --prefix web run test:e2e`（本轮构建的临时二进制） | 最终 **14/14 通过**，约 1.8 分钟 |

E2E 首轮 12 项失败，原因是测试依赖旧布局：登录后默认进入同步源、侧栏 `.sidebar`、标题末尾的句点、类型标签三色、导航内分析图标，以及 320/375 宽下顶栏溢出和图标按钮颜色过渡导致的取色时序。处理方式：需要同步源页的用例改为直接打开 `/admin/sources`；导航断言改为 `.primary-nav`；标题、字重、类型标签（统一中性样式）按新设计更新；删除导航图标对比断言；修复窄屏顶栏（≤480px 账号按钮仅显示图标）和去掉图标按钮颜色过渡。其余断言（分页、日志读取、弹窗焦点、Toast、公开目录布局等）未放宽。

本机预览：在临时目录、`127.0.0.1:18731` 启动隔离实例，用内置浏览器检查了深色/浅色、1440/375/320 宽度下的概览、同步源、批次、分析、设置与运行详情弹窗，以及公开目录页。预览中手动触发过一次 GitHub Release 示例源，任务在扫描阶段即被取消，未下载任何附件（镜像目录 0 B），但可能已向 GitHub 公共 API 发出过只读请求。

### OCR 与覆盖

`ocr review` 外部模型调用全部返回 HTTP 401（凭证被拒），未产生任何审查结果，不计为通过。改用 OCR 代理审查模式：`ocr delegate preview -c 4fb811c` 确定 24 个可审查文件，`ocr delegate rule` 分为两组规则（21 个 Vue/CSS/HTML 文件使用正确性、安全、性能、可维护性、测试覆盖规则；`router.ts`、`theme.ts`、`main.ts` 使用拼写、死代码、类型与空值检查规则），由当前会话逐文件审查。覆盖：24/24 已审查，0 跳过，覆盖率 100%。OCR 排除项：README、DESIGN、ACCEPTANCE（不支持的扩展名）、6 个 E2E 用例（默认排除路径）、`favicon.ico`（二进制）、`logo.svg`（不支持的扩展名）、`AnalysisIcon.vue`（已删除）。

审查发现并已修复：

- 中：概览页在后台标签页仍每 30 秒读取最多 5 页运行记录——改为页面可见时才刷新，返回前台立即校对。
- 中：服务端暂时不可达时概览每 30 秒弹出一次错误提示——改为每段连续失败只提示一次。
- 中：运行阶段改为编号后，已完成步骤仅靠颜色区分——运行详情与概览阶段条增加读屏可读的“已完成”，概览当前步骤补 `aria-current`。
- 中：时间轴每个任务条都是 Tab 停留点，最多可达数百个——任务条改为仅鼠标快捷入口（`tabindex=-1`），同一任务仍可从批次记录键盘访问。
- 中：窄屏导航菜单无法用 Esc 关闭——Esc 关闭并把焦点还给菜单按钮。
- 中（测试覆盖）：概览页缺少 E2E——在主流程用例中增加泳道数、需要处理、运行中/排队列表与停用源断言，并纳入 1024/736/375/320 宽度无横向溢出检查。
- 低：删除无用的 `.source-mark`、`.eyebrow` 样式；结束态颜色判断改为查表，去掉嵌套三元表达式。

修复后重新执行格式检查、Web 构建与完整 E2E：14/14 通过。

### 未验证项

- 仅在 Chromium（Playwright 与桌面应用内置浏览器）验证；Safari、Firefox 与真实移动设备未测。
- 概览时间轴最多读取近 24 小时最近 500 个任务，超出时页面提示截断；高频任务下的渲染性能未做压测。
- 时间轴网格使用启动时的 UTC 偏移，夏令时切换当天的刻度位置可能偏移一小时（与 `format.time` 的回退行为一致）。
- Linux 目标环境与实际发行版未参与本轮界面验收。

## 问题.md 核查与修复（2026-10-10）

本节是当前工作区结论。逐项核查了用户提供的 `问题.md`（H1–H6、M1–M16、L1–L23，共 45 项），保留该文件原文。结论分为确认缺陷、部分属实、功能边界和有依据的设计选择，未把所有建议直接当作正确实现。以下历史验收仅代表各自日期的代码与环境，不能代替本轮验证。

### 逐项结论

| 编号 | 核查结果与处理 |
| --- | --- |
| H1 | 属实。Reposync 关闭元数据时原本仍要求 repodata。增加仅包文件发布模式，去除克隆来的旧 repodata，页面/README 明确该模式不保证可直接安装的 DNF 仓库；完整模式继续校验元数据。 |
| H2 | 属实。Release 中列出不存在的未压缩形式是合法情况，按全部条目必须存在会误拒绝。校验实际存在的索引、哈希及其包引用，容许压缩副本和部分架构；全部索引缺失仍拒绝。优先 InRelease，不把其他目录中同名 Release 文件当仓库声明。未声称验证 APT 签名。 |
| H3 | 属实。服务中断原本长期占用源并阻塞 cron。SERVICE_INTERRUPTED 在 ID/类型/dest 仍匹配且启用时自动排队，沿用累计预算、重试数和等待期限；耗尽预算转超时。暂停和其他故障中断不自动运行。 |
| H4 | 属实。重新添加不同 ID、相同 dest 可遇到旧内部链接无新 ID 记录而启动失败。启动/重载共用 Reconcile，可接管未配置旧源的兼容发布代；同 dest 多份记录优先选已有链接指向的代，否则选最新代。 |
| H5 | 属实。增加来源失败与本地失败的区分；Unknown module、认证/来源协议错误等可切换备用来源，不必先消耗 retry_count，本地磁盘/权限失败仍停止。 |
| H6 | 属实。动态添加已有真实目录此前未执行导入。配置保存/重载现在恢复发布记录、校验导入并注册路由，Enqueue 与配置接入串行。YAML 和文件系统不是一个事务：后续接入失败明确返回“配置已保存/载入”，修复后可重载，不伪称整体回滚。 |
| M1 | 属实。interrupted 不再写入一次性最终结果，不计为分析失败、不触发终态通知；继续后的真实终态正常通知。遵循项目“尚未上线、无旧测试数据库迁移”的协议，未改写旧测试库中已经生成的通知结果。 |
| M2 | 部分属实。发布前仓库不一致此前不参与重试，已纳入受预算约束的重试/备用源流程。未将 exit 23/24 一律视作成功：上游缺失或不可读可能留下不完整镜像，失败保留旧代比无条件发布更合理。 |
| M3 | 部分属实。拒绝没有模块的 rsync URL，要求模块内子目录带 `/` 并在 UI 提示；模块根本身不要求尾斜杠，原文“所有路径都需要”过于绝对。 |
| M4 | 属实。全局登录计数改为连接 IP 分桶（最多 4096 桶、一分钟窗口），成功登录重置。不信任伪造的 X-Forwarded-For；反代后的共享 IP 限制明确记录。 |
| M5 | 部署限制属实。README 明确不支持 HTTPS 终止后 HTTP 回源的管理模式，可使用 TLS 透传或 HTTPS 回源且保留 Host/Origin。未擅自信任代理头或新增可信代理协议；HTTP 回源支持仍是功能扩展。 |
| M6 | 部分属实。无 Token 的配额和大仓库成本确实存在，限流已按 reset/retry-after 退避且计入预算。保留分页读取以保证 published_at 选择及附件完整性；API 文档未承诺只取前几页即可得出精确最新发布时间。几百个 Release、每四小时一次不必然耗尽每小时配额。100 页上限保留并明确报错。 |
| M7 | 属实。附件正文读取加入五分钟空闲期限，取消网络读取、保留暂存身份和正文，归为可重试并可 Range 续传；不把本地写盘/哈希时间当网络空闲。测试用受控服务器缩短期限验证。 |
| M8 | 属实。现有 prune 开关同时清理保留 tag 内本轮未选中的旧附件/侧车/片段，所有下载成功后才执行；不跟随伪造的 tag 目录符号链接，失败保留旧内容。 |
| M9 | 属实。公开项目列表去掉逐源 COUNT/SELECT，改为最新任务及必要的历史成功任务最多两次批量索引查询，使用一秒共享缓存和请求取消。文件正文下载热路径仍不访问数据库。 |
| M10 | 线性全树成本属实，但“百万文件耗时数十分钟”没有本轮实测支撑。未为降低开销跳过持久化/一致性检查，也未引入影响整个挂载点的 syncfs 或破坏 24 小时回退的代数限制。百万文件、inode 压力及混合下载负载留作 Linux 待验，不宣称已优化此项。 |
| M11 | 属实。重新添加/改 dest 立即恢复路由并复用当前代，移除确认属于本源的旧别名。删除源仍按既定协议保留文件；README 增加停机、备份、精确解除已删除源发布记录、交给 GC 回收的步骤，未加入自动删除用户镜像的行为。 |
| M12 | 属实。DNF 私有目录移入任务 work 隐藏子目录，重试前及执行后清理；启动在 worker/探测运行前清理旧式 dnf-private 和探测目录，仅匹配保留前缀，未知目录保持不动。 |
| M13 | 属实。CLI 实际构建版本注入 App，system/info 与设置页使用该值，去除硬编码版本。 |
| M14 | 属实。先加载证书、绑定监听再启动 worker；关闭先封闭管理入口/排空 HTTP，再停止任务与通知。增加监听失败仍保持 queued 的回归。 |
| M15 | 缺少通知代理与 Webhook 签名属实，属于尚未实现的功能；不同地区一定不可达的说法没有本轮网络实测。README 明确直连、不读取 GitHub/系统代理、无 HMAC/自定义鉴权头及部署要求；本轮未新增这些配置/功能，未向外部渠道发送消息。 |
| M16 | 属实。Rsync 通知准确区分含目录的条目总数与实际传输普通文件数，不再用相减表示“未完成”；Reposync 明示不提供可靠包数。 |
| L1 | 部分属实。修复隐藏项占用 1000 项额度；保留有界读取，明确搜索/排序仅针对已列出的子集。未采用“读取所有名称内存可控”这一未经规模验证的假设，未新增分页。 |
| L2 | 现象属实、建议不采纳。同大小/mtime 的内容可能不同，仅用路径/大小/mtime 会错误返回 304；新代使用不同 ETag 是不可变代隔离策略。 |
| L3 | 现象属实、策略保留。统一 attachment 防止同源不受信任内容内联执行；仅凭扩展名或假定 CSP 足够而放宽，不是本轮缺陷修复。 |
| L4 | 属实。Linux 用 Frsize（无有效值时回退 Bsize），Darwin 保持 Bsize。运行中只有 ErrLowSpace 取消任务；普通探测错误记日志，新任务仍拒绝未知容量准入。 |
| L5 | 属实。interrupted 排除出清理候选；删除成功但保存失败时返回实际删除数量，API 错误响应也携带 cleared，保留清理与其他任务控制并行回归。 |
| L6 | 属实。恢复改为 ID 游标每页 100 条，无总条数截断；不重写未变化的暂停等记录。新增跨页恢复、暂停、预算耗尽、类型变更、持久化失败中断回归。 |
| L7 | 属实。日志写入器跨 Write 保留凭据前缀，最长匹配替换，结束 Flush；逐字节分割位置测试覆盖中文及重叠密钥。 |
| L8 | 属实。128 KiB 读取末尾退到完整 UTF-8 字符边界，next_offset 按实际消耗字节推进。 |
| L9 | 主要是文档边界。GitHub 确实直连/显式代理；DNF/rsync 继承环境、公钥下载读环境代理。已区分说明，未改变系统工具的环境协议。 |
| L10 | 属实。DNF HTTP 401/403/404 等归为上游错误，本地权限、只读/空间错误保持 LOCAL_TRANSFER；测试核对错误码和重试性。 |
| L11 | 事实属实，但与前一轮用户确认的“新库、删除空迁移设施”决定一致。未恢复已删除 schema 版本表；文档指出未来兼容升级前必须另行设计迁移。 |
| L12 | 属实。通知/源保存不丢弃设置草稿；其他管理员修改实际设置时保留草稿并提示冲突，保存使用草稿 revision，用户显式丢弃后才载入最新值。E2E 覆盖合并和冲突。 |
| L13 | 属实。补充 sha/sha1 元数据一致性哈希，仍校验实际内容；不把 SHA-1 当作安全签名保证。 |
| L14 | 保守新代的现象属实，尚无本轮代增长实测。repomd 摘要不能证明所有包内容未改；保留 inode/ctime 检查，未采用可能误复用旧正文的捷径。 |
| L15 | 默认容器 root、无固定 HEALTHCHECK 属实，但“不支持非 root”不成立（历史验收已有 UID 5000）。README 增加 --user/卷权限和部署探针说明；任意端口/TLS 配置下不硬编码健康检查。 |
| L16 | 属实。五份已完成计划移到 docs/archive/plans 并标记历史状态，修复链接及 IMPLEMENTATION/README/DESIGN 的描述。历史版本记录不伪改成当前版本；当前工具链保持 1.27.2。 |
| L17 | 平台/文件系统边界属实。README 首屏明确仅 Linux/Darwin 与 reflink 硬要求；未承诺其他 Unix 或添加与项目约束相冲突的完整复制降级。 |
| L18 | 行为属实，属于用户已确认的浏览器通知时区协议，保留；调度/管理台仍使用启动时区。 |
| L19 | 属实，但标准排除语义不代表应自动删旧内容。UI/README 增加排除不会删除既有文件的提示，保留非破坏性行为。 |
| L20 | 部分属实。admin/api/favicon 仅限制顶级，删除重复 GPG 校验。不同类型选项随编辑器切换保留是现有协议，文档明确只有当前类型选项生效，未改成拒绝 UI 自身合法草稿。 |
| L21 | 属实。仅 sql.ErrNoRows 返回 404；数据库失败/超时返回 503，已验证故障读取不伪装成不存在。 |
| L22 | 属实。下载超过准入阈值时排队任务显示 waiting_downloads，并记录一次进入该阶段的事件，保持排队不耗预算。 |
| L23 | 现象属实、建议不采纳。拒绝保留/危险 tag 和附件名是上一轮明确的安全修复，不能静默跳过后报告完整同步成功。 |

外部依据：[Debian 仓库格式](https://wiki.debian.org/DebianRepository/Format) 对未压缩索引校验项与实际文件区分（搜索索引可读，正文抓取本轮返回 403）；[rsync 官方手册](https://www.samba.org/ftp/rsync/rsync.1) 说明模块根无需尾斜杠、子目录尾斜杠改变拷贝语义、23/24 表示部分传输；[GitHub Release API](https://docs.github.com/en/rest/releases/releases#list-releases) 与[附件分页 API](https://docs.github.com/en/rest/releases/assets#list-release-assets) 提供分页接口，未提供本项目所需“前几页覆盖全部最新发布时间”的保证。

### 验证与未验证项

本机 Go **1.27.2 darwin/arm64**，测试显式使用 `.cache/rsync/rsync-3.5.1/rsync`。使用临时目录、隔离端口、受控 HTTP/rsync 上游和本地通知接收器；未启动示例完整发行版同步、未修改用户镜像/凭据、未推送或发布。证据均在忽略目录 `test-results/issues-20261010/`。

| 检查 | 结果与证据 |
| --- | --- |
| Web 格式、TypeScript、Vite | format:check 与 build 通过；`web-format-check.txt`、`web-build.txt`。Go 编译前已构建 web/dist。 |
| 全量 Go race | `go-race-final.json`：9 个含测试包通过，148 个顶层测试、含子测试共 258 个通过，0 测试跳过、0 race；web 包无测试文件的包级 skip 不算漏测测试。 |
| 审查后 Go 复验 | `go-race-review-fixes.json`：runs/sync 两包 55 个顶层、含子测试 114 个通过，无跳过；`go-mirrors-review-fixes.txt`：新增启动遗留目录清理回归和完整 mirrors race 通过。 |
| vet / Linux 编译 | `go-vet-final.txt`、`linux-build.txt` 均成功；Linux amd64 CGO_ENABLED=0 交叉编译，仅证明编译，不代表 Linux 运行或 Frsize 实测。 |
| 浏览器 E2E | `e2e.txt`：14/14 通过，约 1.8 分钟；含设置草稿保留/冲突、真实隔离 rsync、通知、公开目录、分页和响应式布局。后续仅后端恢复/错误归类调整，重新构建二进制并复跑同步和通知两项，2/2 通过（38.1 秒），见 `e2e-review-fixes.txt`。抽查系统设置桌面及 375px 截图，实际版本正常显示，无阻塞性溢出。 |
| 差异检查 | `git diff --check` 通过；归档计划只增加历史状态说明和修正相对链接，保留历史版本事实。 |

初轮测试曾因旧 interrupted 清理/恢复断言、APT 缺失引用提示和列表回调约定变化失败（其中旧清理等待测试被停止）；这些断言已改为新的协议，保留了清理不阻塞其他任务的覆盖。首次全量 race 中，新数据库故障测试误用了仍在内存的 queued 任务，得到 200；改用不在内存的 ID 才能验证数据库故障，最终复跑通过。曾出现一次 Go timeout 参数拼写错误、一次 Prettier 工作目录错误及一次错误的 CleanLog 包引用编译失败，已修正；不把这些失败当作通过。外部 OCR 的失败单独记录在下一节。

本轮未重做 Linux 原生运行、真实 DNF4/完整发行版安装、Linux sendfile/Frsize 观测、生产 TLS 反代网络、通知外网可达性、百万文件/inode/混合下载性能和断电恢复。历史验收不能替代修改后生产验收。性能改动 M9 依据查询结构及回归验证，未报告未经测量的吞吐提升；M10/L14 仍是规模验证项。M5 HTTP 回源可信代理、M15 通知代理/HMAC、L1 全目录分页未实现，仅明确功能边界。

### OCR 审查与覆盖

执行了 `open-code-review` 外部审查入口，退出码 1：`no valid LLM endpoint configured`，因此没有外部模型审查结果，也没有因该调用发送源码。随后使用 `open-code-review-delegate` 的本地 `preview` / `rule`，由当前 Codex 按解析出的 Go/TypeScript/通用规则逐文件复核。并非独立审查模型通过。证据：`ocr-external.txt`、`ocr-preview-final.json`、`ocr-rules-final.json`、`ocr-coverage.json`。

最终清单共 **53 个 path/status 条目**：OCR 自动纳入 23 项，全部审查（23/23，100%）；自动排除 30 项，另外人工审查 29 项，1 项用户原始 `问题.md` 作为需求证据读取但不作为实现代码审查。总体实施/文档覆盖 52/52，未跳过任何本轮新增或修改的源码。排除原因是测试/E2E 的 default_path、Markdown 的 unsupported_ext，以及旧平台文件 deleted；不将工具默认排除等同于完成审查。

| OCR 纳入文件（均已审查） | 重点 |
| --- | --- |
| cmd/mirrordock/main.go | 构建版本传递、启动日志真实性 |
| internal/app/app.go | 监听/关闭顺序、锁顺序、批量缓存、HTTP 错误、版本 |
| internal/app/auth.go | 有界按 IP 限流、不信任转发头 |
| internal/config/config.go | 模块/目录 URL、顶级路由与已有配置兼容 |
| internal/database/database.go | 真正终态、游标恢复、清理和批量查询 |
| internal/mirrors/http.go | 目录配额、取消、正文下载不访问 DB |
| internal/mirrors/listing.go | 批量信息替代旧单源接口 |
| internal/mirrors/mirrors.go | 导入/恢复/别名归属、代引用、错误与锁顺序 |
| internal/mirrors/storage.go | 启动临时目录清理边界、未知目录保留 |
| internal/mirrors/validate.go | 仅包模式、APT 压缩/部分架构、SHA-1、错误类别 |
| internal/mirrors/space_darwin.go | 保持 Darwin 单位 |
| internal/mirrors/space_linux.go | Frsize 与跨平台编译 |
| internal/notifications/message.go | Rsync/Reposync 计数含义 |
| internal/runs/analytics.go | 中断不混入失败/耗时样本 |
| internal/runs/runs.go | 分页恢复、预算、备用源、日志、配置串行 |
| internal/sync/github.go | 空闲取消定时器生命周期、续传、限流、清理 |
| internal/sync/process.go | 本地/上游诊断优先级 |
| internal/sync/runner.go | rsync 来源失败、DNF 私有路径及旧元数据 |
| web/src/app/format.ts | 等待下载负载阶段 |
| web/src/features/analytics/Analytics.vue | 统计口径匹配后端 |
| web/src/features/runs/RunDetail.vue | 未执行阶段的展示 |
| web/src/features/settings/Settings.vue | 草稿合并、revision 冲突、实际版本 |
| web/src/features/sources/SourceEditor.vue | 元数据/排除/地址提示准确性 |

补充审查的测试/E2E 共 14 项：`internal/app/{app_test,auth_test}.go`、`internal/config/source_url_test.go`、`internal/mirrors/{listing_test,validate_test,issues_test}.go`、`internal/notifications/notifications_test.go`、`internal/runs/{analytics_test,logs_test,runs_test,issues_test}.go`、`internal/sync/{github_test,runner_test}.go`、`web/e2e/notifications.spec.ts`。删除的 `internal/mirrors/space_unix.go` 已核对由两份平台文件覆盖。文档共 14 项：README、DESIGN、IMPLEMENTATION、ACCEPTANCE，以及五份计划的原路径删除和 archive/plans 新路径（2026-10-02-release-preflight、2026-10-03-download-logs-linux、2026-10-03-review-fixes、2026-10-03-storage-reflink、2026-10-09-admin-performance）。

审查中修复了：AfterFunc 停止失败时等待回调结束，避免跨读取的迟到取消；历史发布记录选择不确定性；旧别名同步错误被吞；启动恢复重复持久化；DNF 本地权限误归上游；日志清理 API 不回传实际部分删除数；配置已保存但接入失败的含糊错误；文档归档链接。未发现尚未处理的本轮确定性正确性/安全性阻塞项；上表保留的功能边界与未测规模风险不算已验证通过。

## 1.0 发布前最终验收（2026-10-09）

本节是 2026-10-09 当时的交付结论，不代表后续改动的工具链检查或验收结果。检查包括全仓测试、核心运行/发布/鉴权/数据库/连接池/前端请求生命周期审查，以及本轮全部代码差异复核。应用版本已更新为 **1.0.0**，工具链按用户确认统一升级为 **Go 1.27.2**。本轮已识别的代码阻塞问题均已修复，下面列出的门禁通过或已完成明确告警判定。未提交、推送、打标签或发布。

测试通过 SSH 在用户指定的 `mirrordock` 主机原生执行，隔离工作区为 `/home/ops/work/mirrordock-release-20261009`。系统为 AlmaLinux 10.2 x86_64、内核 6.12.0-211.22.1.el10_2、XFS、20 个逻辑 CPU、约 3.54 GiB RAM；`lscpu` 明确报告 **KVM**，不是裸机验收。SELinux 为 **Permissive**。原生工具链为 Go 1.27.2、Node 24.21.0、系统 rsync 3.4.4、DNF 4.20.0。Go/Node 官方压缩包均核对官方 SHA-256。只使用临时数据、隔离端口、受控 rsync/RPM 上游和小型公开 GitHub 附件，不修改用户镜像或应用凭据。

### 本轮修复

| 问题 | 修复与回归 |
| --- | --- |
| 旧 Go 标准库安全问题 | Go 1.27.1 扫描报告标准库调用告警；按用户确认升到 1.27.2，同步 go.mod、Dockerfile、协作规则与文档。[官方发布记录](https://go.dev/doc/devel/release#go1.27.2)；未将扫描可达性当作每项已可实际利用 |
| 通知队列反复扫描完整历史，占用写连接 | run_results 增加独立自增插入序号，按序号每页最多 100 条前进，保留 run_id 唯一与首个终态；入队全部成功后推进持久游标，失败重放由唯一键去重。覆盖跨页、晚完成、重启和中途写入失败 |
| 通知读取与并发边界 | 记录计数和页内容共用五秒可取消的只读事务；占满单写连接时仍能读取。启用边界的 run ID 和结果序号在同一 SQL 快照取得；设置视图和 revision 也来自同一配置快照，避免并发重载错配 |
| 前端旧会话及卸载请求 | 退出统一中止会话 API；处理 401 和 JSON 结果前再次检查取消，旧 401 不会退出新登录。环境检查、通知设置/分页卸载及换页取消旧请求；新增旧会话回归 |
| 页面保存覆盖部署日志配置 | 浏览器保存保留 log 字段；修改 YAML 后重载并提示重启。测试覆盖注释保留、API 保存隔离和启动值不变 |
| GitHub tag/附件与内部文件冲突 | 拒绝 `.mirrordock*`、`.rsync-partial`、`.~tmp~`、`*.part`，防止覆盖身份侧车、暂存文件或发布隐藏文件。10 个 tag/附件组合回归 |
| GPG 公钥 HTTPS 降级 | 公钥下载复用受限重定向策略，禁止 HTTPS 跳到 HTTP。受控 TLS 测试确认明文目标没有收到请求 |
| APT 校验被静默截断 | 超过 8 MiB 的 Release 明确失败，不能只检查前缀后当成完整校验通过；测试将缺失引用放在读取上限之后 |

按“尚未部署，无需兼容”的要求删除发布前历史 URL 脱敏迁移和空转的 schema_migrations 版本表；新运行快照继续防御性脱敏。**首次上线使用新的 state 数据目录**，不承诺兼容旧测试数据库。已有镜像导入、当前版本重启恢复、日志保留和失败保留旧发布代的行为仍在回归范围内。

### 执行结果

本地证据根为 `test-results/release-1.0/`，远端简要输出已归档在其中的 `linux/`，脚本与详细夹具保留在上述远端工作区。证据目录被 Git 忽略。

| 检查 | 结果及证据 |
| --- | --- |
| Web 格式、TypeScript、构建 | 本机与 Linux 均通过；`web-format-final.txt`、`web-build-final.txt`。入口 JS 126.25 kB，Vite 估算 gzip 49.71 kB；这不是服务端动态压缩承诺 |
| Linux vet / 全量 race | Go 1.27.2 原生编译，8 个 internal 包、132 个顶层测试、含子测试共 228 个通过，**0 测试跳过、0 race 报告**；cmd/web 无测试文件单独计。`linux/linux-race-final.json` |
| 本机补充 race | 最终全仓运行出现一次 app 登录请求五秒超时，其余包通过；随后完整 app 与 notifications 在 race 下复跑通过。保留 `local-race-final.json` 与 `local-race-recheck.txt`，不把初次失败写成通过 |
| Linux 浏览器 E2E | **14/14 通过，约两分钟**，含真实隔离 rsync、备用来源、发布/日志/分页/通知、旧响应、键盘焦点和移动布局。`linux/e2e-linux-final.txt`；生成 77 张截图，抽查桌面源列表、Release 分析、375px 通知与 320px 公开目录，未发现阻塞性错位 |
| Linux HTTP / HTTPS | 完整正文、HEAD、ETag/304、单段及非法 Range、24 个并发 Range 通过；TLS HTTP2 协商通过。strace 观察到 HTTP/1.1 成功 sendfile **43 次**、HTTPS **0 次**，符合设计。`linux/http-final.txt` |
| 混合负载与恢复 | 5003 文件、2050 条历史日志，12 个下载客户端和 8 个慢客户端，与同步/暂停/继续/清日志并行；无变化代复用、硬链接、预算暂停、清理和强杀后继续均通过。`linux/stress-final.txt` |
| RPM 完整路径 | AlmaLinux 10.2 x86_64 的 DNF4 reposync，实际签名 noarch RPM 同步及私有 installroot 安装通过；未签名包拒绝，旧元数据/正文保留，宿主默认 RPM 密钥库不变。`linux/rpm-final.txt` |
| 磁盘不足 | 隔离 4 GiB XFS loop：不足时排队不耗预算、空间恢复自动启动、运行中低余量触发 DISK_SPACE、保留旧代，并在隔离卷观测 ENOSPC；卷已卸载。`linux/space-final.txt` |
| 冷/热缓存 | 128 MiB 文件、三轮单文件 fadvise + mincore；热缓存读盘 0，冷轮实际读取约 131 MB，未全局 drop_caches。`linux/cache-result.txt`；仅客体页缓存测试，不宣称清空宿主/磁盘缓存 |
| 离线备份恢复 | 同路径恢复 2054 条事实，发布代 ID、正文 SHA 与通知密钥保留。`linux/backup-result.txt`；不是跨路径迁移或断电测试 |
| systemd | 非 root、NOFILE=8192、NoNewPrivileges、ProtectSystem=strict、ProtectHome、PrivateTmp 下启动/下载/SIGTERM 均通过；临时 unit 已停止，夹具归档到远端 evidence/systemd-sandbox。`linux/systemd-result.txt` |
| 真实 GitHub Release | 用授权 SOCKS5h 代理与临时 Token 下载公开 `jqlang/jq` 的 jq-1.8.2：jq-linux-amd64 与 sha256sum.txt；正文 2,267,912 字节，SHA-256 同时匹配 API digest 和上游校验文件。第二次复用两项、保持发布代。运行数据库/日志/API 不含 Token，临时 Token 文件与 YAML 已删除。`linux/github-final.txt` |
| 实际 Dockerfile | Linux amd64 从 Node → Go 1.27.2 → Rocky 9 多阶段构建通过；默认入口/默认配置路径、UID 5000、只读根、隔离网络、/tmp 与 /var/tmp tmpfs、鉴权、嵌入资源、系统工具、HEAD/Range/ETag 和退出码 0 均通过。`linux/docker-build.txt`、`linux/docker-runtime-recheck.txt` |
| 制品一致性 | 当前 132 个源码/构建文件 SHA-256 与 Linux 验收副本一致；`source-manifest.json`、`source-and-cleanup-check.txt`。原生二进制 SHA-256 为 `42144d58fe085d88a6eefd384b7a96a1d5667965f0ccaad2eb6303257fb45c23` |

### 数据库与压力实测

SQLite 保持 WAL/FULL、单写连接与最多四条只读连接；读事务、连接重建 PRAGMA、索引访问、低 FD、根租约、不可变代和并发发布既有回归均包含在 Linux 全量测试中。公开文件下载热路径仍不访问数据库。

100,000 条已处理终态的热库空扫描，三轮中位数：原历史反连接查询 **74.041 ms/次**，插入游标查询 **9.834 μs/次**。这是同机 SQL 对照，结果行均为零，不等于页面或下载整体加速比例；原始值见 `linux/notification-bench-linux.txt`。macOS 对照为约 105 ms 与 9.108 μs，单独记录在 `notification-bench-local.txt`。

最终混合负载：管理 API P50 **7.63 ms**、P95 **15.29 ms**、最大 **16.64 ms**；RSS 峰值 **33.52 MiB**、FD 峰值 **50**，CPU 均值 **0.47 核**，清日志耗时 **2.22 秒**。10 秒窗口完成 4850 个 8 MiB 下载，另有慢客户端逐段校验。回环网络与客体缓存的合成吞吐不能替代物理网卡、公网、真实多 TB 镜像或生产容量结论；测试期间另有容器构建活动，数据作为当前主机验收观测，不作严格硬件基准。

### 供应链与审查

`go mod verify` 通过。Linux 首次官方 Go 模块代理访问超时，原生测试改用本机已验证模块生成的临时 vendor，vendor 不提交；随后实际 Docker 构建通过 Goproxy.cn 拉取模块，保持 Go 校验数据库。Rocky 构建使用南京大学镜像，保持 RPM 签名校验；运行镜像查询发行版 available security advisories 无条目，不等于对全部 OS 包完成独立 CVE 扫描。Docker 镜像 ID 为 `sha256:161120ff3777cb52c8628a5adb89f5c273c26c3800cd3674f6ce204aa3575339`，基础镜像 digest 与版本在构建日志中。

本机和 Linux 使用 npm 官方 registry 在线审计均为 **0 漏洞**，包含开发依赖；Linux 实际安装的 **52 个包 registry 签名通过、33 个包 provenance attestation 通过**。本机为 52/32，平台依赖不同，未混成一个计数。证据 `linux/npm-audit-linux.json`、`linux/npm-signatures-linux.txt`。

Go 官方漏洞库源码扫描：Go 1.27.2、linux/amd64 下 **0 可达漏洞、0 导入包漏洞**；模块级保留 GO-2026-5932（x/crypto/openpgp），项目实际只使用 bcrypt 等包，无 OpenPGP 依赖。生产二进制原始扫描出现一条需要复核的模块告警，详见下一段。

剥离符号的 Docker 制品执行 `govulncheck -mode=binary` 返回 **退出码 3 / GO-2026-5932**，没有将这次命令记成通过。`-mode=extract` 确认未提取符号；govulncheck v1.8.0 在此条件下按模块精度报告全部已知告警，与[官方说明](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck#hdr-Limitations)一致。[该告警](https://pkg.go.dev/vuln/GO-2026-5932)只针对 OpenPGP 包。复核：Linux/amd64、CGO=0 的实际依赖清单不含 OpenPGP，制品中也没有该包路径；从同一 Docker builder 层离线重建保留符号的审计副本，与生产制品的 ELF `.text` 机器码逐字节一致，该副本二进制扫描 **退出码 0、0 符号命中**。因此本轮将该项判定为剥离符号造成的模块级非适用告警，保留原始告警而不替换密码算法或过滤扫描输出。证据：`govulncheck-docker-binary.txt/json`、`docker-vulnerability-extract.json`、`linux-dependencies.txt`、`binary-code-sha256.txt`、`govulncheck-symbols-binary.txt`。扫描器限制不能用作忽略其他新告警的通用理由。

OCR 外部入口实际执行后退出码 1，原因 `no valid LLM endpoint configured`；没有外发源码，未宣称外部独立模型审查通过。按项目流程使用 `ocr delegate preview/rule`，由当前 Codex 审查所有 **17 个 reviewable 文件**；默认排除的 15 个测试、删除文件、go.mod、lockfile 与文档逐项另行复核，总覆盖 32/32、0 skipped，覆盖清单见 `ocr-preview-final.json`、`ocr-rules-final.json`、`ocr-report.json`。审查补强了通知启用边界必须同一快照，以及配置视图与 revision 的一致性。没有遗留本轮已识别的代码阻塞项。

初次失败均保留：本机新 E2E 未切到环境检查页签、Linux Chromium 缺少系统库、真实 GitHub 夹具误填不存在的 retry_delay、只读容器缺少 /var/tmp tmpfs；修正测试夹具/环境后对应完整路径通过。匿名 GitHub 代理出口先遇配额耗尽，随后按用户授权 Token 完成真实测试。工具安装与浏览器运行依赖留在测试主机，未修改其部署安全策略；没有仍运行的本轮应用测试进程或隔离 loop 挂载。

### 未验证的部署条件

本轮不覆盖裸机物理链路、SELinux Enforcing、宿主微码/虚拟化安全状态、真实断电/存储故障、生产百万文件/多 TB/数日长稳、跨路径备份恢复、全部发行版与架构、完整公网发行版同步或外部通知提供商投递。网页交互与移动布局在 Chromium 中验收，不扩大为所有浏览器支持承诺。Go/OS/npm 的安全结论有扫描时点，发布后仍需跟随上游补丁。上述边界与已通过项目门禁应一并阅读。

## 管理界面与查询性能移植（2026-10-09）

范围：本轮工作区的 8 个新增/修改生产源码文件、5 个测试文件，以及 README、设计和计划。参考目录仅用于阅读，未修改；未修改用户配置、镜像或凭据，未推送或发布。所有测试使用临时数据、隔离端口和受控上游。

**结果：本轮实现与验证完成。** 运行详情加入同步阶段、四项统计和折叠区；日志按需读取、终态补读到末尾后停止，隐藏/收起/关闭取消请求。暂停和中断任务仍检测恢复。管理运行读取使用最多四条只读连接，计数与页内容共用事务；最新任务从逐源查询改为单次索引批量读取；SSE 共用最多一秒的序列化快照。写入协议、两同步 worker 与公开下载热路径保持原语义。

### 已执行检查

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check`、`npm --prefix web run build` | 通过，Web 构建包含 TypeScript 检查；先于 Go 编译 |
| `GOCACHE=/tmp/mirrordock-go-cache go vet ./...` | 通过 |
| `go test -race -count=1 ./internal/database ./internal/runs ./internal/app` | 通过；新增读取隔离、连接重建参数、查询计划、SSE 并发合并/失败恢复/等待取消 |
| `go test -race -count=1 ./...` | 全仓通过；指定仓库缓存的现代 rsync，隔离夹具；见 `test-results/admin-go-all.txt` |
| 详情相关 E2E | 6/6 通过；复核后补强首个尝试延后出现的回归，再运行完整套件 |
| `npm --prefix web run test:e2e` | 最终 **13/13 通过，1.8 分钟**；含真实隔离 rsync 失败切换、日志下载与多轮尾部读取；见 `test-results/admin-e2e-full.txt` |
| 详情边界 | 首个尝试在 running 状态稍后出现、保留历史尝试、终态发生在日志读取途中、18 页积压日志、折叠/隐藏停止、返回恢复、终态停止与手动刷新、关闭取消、错误恢复、summary 键盘焦点均通过 |
| 视觉复核 | 已查看桌面和 320px 详情截图；E2E 同时检查 375px，统计为两列且弹窗无横向溢出 |
| 无参考目录独立构建 | 从实际工作区源码生成临时副本，排除参考目录、用户状态、node_modules 和 dist；`npm --prefix web ci --offline` → Web 构建 → Go 构建通过 |
| 独立二进制冒烟 | 空源配置：认证状态、公开首页、管理首页、夹具账号登录均为 200；SIGTERM 退出码 0。清单与结果见 `test-results/admin-standalone.json`、`admin-standalone-smoke.json` |
| 差异与范围 | `git diff --check` 通过；13 个已审查源码/测试的 SHA-256 复核一致；生产源码与构建入口无对参考目录的引用 |

Go 测试使用 `GOCACHE=/tmp/mirrordock-go-cache`；真实 rsync 通过 `MIRRORDOCK_TEST_RSYNC=$PWD/.cache/rsync/rsync-3.5.1/rsync` 指定并加入 PATH。E2E 使用本轮构建的 `/tmp/mirrordock-admin-test`。首次 Go 检查因默认缓存写权限失败，随后在临时缓存重跑；首次 app 测试因沙箱禁止监听回环端口失败，获准后重跑通过。这些初次失败不算通过项。

### 本机查询对照

macOS arm64 / Apple M2 Max / Go 1.27.1，临时 SQLite、热缓存、每源一条 `{}` 快照。命令：`go test ./internal/database -run '^$' -bench BenchmarkLatestRuns -benchmem -count=3 -benchtime=200ms`。旧查询方式与新方式在同一实现、相同只读池中对照；下表取三轮中位数。

| 配置源数 | 逐源查询 | 单次批量查询 | 查询耗时缩短 |
| --- | --- | --- | --- |
| 10 | 0.104 ms | 0.0289 ms | 约 72% |
| 100 | 1.040 ms | 0.114 ms | 约 89% |
| 1000 | 10.667 ms | 0.980 ms | 约 91% |

1000 源时分配约从 1.03 MB/次降为 0.13 MB/次；原始三轮数据见 `test-results/admin-query-bench.txt`。该基准隔离 SQL 往返与结果读取开销，使用极小快照，不含真实 JSON 解析、SSE 网络传输、写入竞争、冷磁盘或同步。不能将其比例解释为整个页面、rsync 或下载吞吐提升。共享帧回归验证同一有效期内 8 个调用者只执行一次读取，未进行生产 SSE 吞吐测试。

### OCR 与覆盖清单

`ocr review --audience agent --exclude 'video-rename/**' --output ...` 因 **未配置有效 LLM 端点**退出，外部 OCR LLM 审查未执行，也没有外发源码。随后使用 `ocr delegate preview --format json` 和 `ocr delegate rule --format json`，由当前 Codex 按规则审查全部 8 个生产差异文件：

- `internal/app/app.go`、`internal/app/progress.go`：共享帧所有权、等待取消、错误不缓存、认证和 SSE 关闭。
- `internal/database/database.go`、`internal/runs/runs.go`：单写/只读连接隔离、每连接 PRAGMA、事务/Rows 释放、请求上下文、索引访问与历史语义。
- `web/src/components/Modal.vue`、`web/src/features/runs/RunDetail.vue`、`web/src/features/runs/useRunDetail.ts`、`web/src/features/runs/run-detail.css`：焦点、窄屏、陈旧响应、终态日志收尾、可恢复任务、显示与生命周期。

`reviewed_files=8 / skipped_files=0 / coverage_rate=100%` 只指本轮 8 个生产文件。OCR 默认排除的 5 个 Go/E2E 测试另行逐项复核；Markdown 另行检查。参考目录因未修改且非交付依赖排除。审查修正了首个尝试稍后出现时日志不启动的边界，并补充回归；折叠后的日志错误不再显示无效重试按钮，保留日志 DOM 与跟随设置。未遗留本轮已识别的正确性、安全性或性能阻塞项。机器记录见 `test-results/admin-ocr-preview.json`、`admin-ocr-rules.json`、`admin-review-coverage.json`。

### 验证边界与后续范围

本轮未重复 Linux/EL、真实生产多 TB/百万文件、冷缓存、断电、外部通知或在线依赖漏洞审计。离线 npm 安装输出的审计提示不视为最新在线漏洞检查。查询仍需等候引擎现有状态锁；5 秒期限约束数据库操作，不承诺所有锁等待都在 5 秒内结束。尚未改动检查点持久化锁、增加统计缓存/批量操作 API 或扫描并发，这些需单独测量并验证一致性。参考项目可以删除，不影响本轮独立构建和运行。

## 历史完整验收（2026-10-02 至 2026-10-03）

验证日期：**2026-10-02 至 2026-10-03**。范围为当前完整工作区，包含开始时已存在的后端统计、时间筛选、共享开关和 UI 对齐改动，以及本次新增修复；保留用户配置协议与已有修改。未推送、发布正式服务、发送外部消息或启动完整发行版同步。

**当轮结论：R1–R7 已修复；下载与日志清理优化也已完成，最终程序在用户提供的 AlmaLinux 10.2 x86_64 虚拟机验收通过。** 当轮实现、对照数据、测试与未验证边界见下节；其他章节保留为对应版本的历史证据。

## 下载、日志优化与目标 Linux 验收（2026-10-03）

用户授权一起完成下载路径、日志清理优化及 SSH 别名 mirrordock 对应虚拟机验收。此次没有修改配置字段、依赖、同步协议或用户镜像/凭证；未创建子代理、推送、发布正式服务或发送外部通知。测试只使用新建隔离目录、回环端口、受控上游和临时 XFS 镜像盘。最终 Linux amd64 程序 SHA-256 为 `fb87163185c83ff9ef4f20f11d07b75b03e6b3ece27c67d244ecae341984db38`；产物与全部测试二进制在执行前逐项验 SHA-256。

### 实现与审查

- 下载采用一个受限镜像锚点与最多 32 项代根 LRU，根与锚点占用不超过预留 FD 的四分之一；软限制 128 时最多缓存 15 项。借用覆盖文件打开，文件和代引用仍持续到请求结束；缓存全被借用时使用受请求配额约束的瞬态根。回收/关闭释放缓存，冷查找在缓存锁外进行；路径在路由安装时验证并固定。元数据用结构键，命中不格式化 ETag 键，并缓存 Content-Disposition；仍使用真实文件、ReaderFrom、os.Root，无下载热路径数据库读取、正文哈希或动态压缩。
- 日志清理以开始时最大任务 ID 为上界，每页 100 条，用主键游标读取终态事实。实际查询计划最初选择 runs_state_id 并临时排序，已补强 `NOT INDEXED`，Go SQLite 实测为 `SEARCH runs USING INTEGER PRIMARY KEY (rowid>? AND rowid<?)`，无 TEMP B-TREE，避免每页重复排序剩余历史。删除/保存在 Engine.mu 外进行，按任务清理租约协调 resume/cancel，其他任务继续运行；请求/服务取消停止后续工作，已删除部分保存，删除/保存失败可重试，已打开日志导出保持原长度。
- 新增 6 个顶层永久测试，另扩充既有低 FD 回归：缓存满且全借用、热根复用、闲根驱逐、活动租约关闭、并发/逃逸；清理期间其他任务控制与同任务恢复、205 条游标上界、取消/删除/保存失败重试；230 条含活动任务的主键分页与实际查询计划。已有日志导出、150 次发布/149 旧代/128 FD、旧包回退、Range、硬链接与 GitHub 无变化测试继续通过。

按 open-code-review 实际运行外部 review 入口，退出码 1，原因为 no valid LLM endpoint configured；没有调用外部模型或发送源码。使用项目允许的本地 delegate preview/rule，由当前 Codex 复核全部 7 个新增/修改生产 Go 文件；4 个测试与 4 个文档/计划排除项另行审查，忽略目录的 Linux 验收脚本也核对了目录/端口隔离、受控上游、资源清理、断言与统计口径。覆盖清单、规则、源码哈希和结论见 test-results/linux-opt-ocr-{preview-final,rules-final,report}.json。审查补强了缓存冷 I/O 的锁范围及清理查询计划，最终没有遗留已确认的正确性、安全性或性能阻断问题。

### 最终验证

目标环境为 AlmaLinux **10.2 x86_64**、Linux 6.12.0-211.22.1.el10_2、20 个逻辑 CPU / 3.5 GiB RAM；根盘为约 95 GiB XFS，reflink=1。系统 rsync 3.4.4、DNF4 4.20.0、RPM 4.19.1.1，SELinux 当前为 **Permissive**，不宣称完成 Enforcing 验收。为测试安装 strace、rpm-build、createrepo_c、rpm-sign 及所需依赖；首次被 kernel-srpm-macros 排除规则挡住，仅本次工具安装使用 disableexcludes，未改全局规则或升级内核。

| 检查 | 实际结果与证据 |
| --- | --- |
| Web 格式 / 构建 | 通过；linux-opt-web-{format,build}.txt。先构建 Web 再编译嵌入包；前端源码未改变 |
| 本机 Go vet / 全量 race | Go 1.27.1、8 个 internal 包全部通过，现代 rsync 启用；linux-opt-go-{vet,race}-complete.txt。race 为 macOS arm64，不扩大成 Linux race |
| 程序构建 | 本机及 Linux amd64 / arm64 成功；amd64 实际运行，arm64 本轮仅交叉构建 |
| 最终完整 E2E | **11 passed、0 skipped**；linux-opt-e2e-complete.txt，使用最后 SQL 补改后的程序 |
| Linux 全包原生测试 | 离线交叉编译 CGO=0 的 8 包测试二进制在目标机执行，**124 个顶层测试通过、0 skipped**，真实 rsync 为 /usr/bin/rsync；latest/native-{run.txt,results.json}。包含 Linux 进程/TCP 实现及新的主键查询计划回归，不带 race |
| 实际 HTTP / TLS / H2 | 8 MiB 正文 SHA-256、HEAD、ETag 304、Range 206/416、24 请求/12 并发范围核对及实际 HTTP2 协商全部通过；最终 HTTP1.1 strace **43 次成功 sendfile**、HTTPS **0 次**，SIGTERM 退出码 0。前轮 39/41 次记录为历史，不用次数衡量吞吐 |
| 同步与下载并行 | 5003 个受控上游文件；真实网络失败切换、无变化代 ID 复用与硬链接、暂停不计预算、继续与变化发布均通过。12 个下载客户端 + 8 个慢客户端，每慢客户端按 64 KiB/50ms 验证 8 MiB 前缀，清理与同步并行时所有正文核对成功 |
| 批量日志清理 | 2050 个历史任务日志与其余终态尝试共清理 **2057** 项，用时 **2.33s**；记录与错误摘要保留。清理/下载/同步期间管理请求 P50 **9.27ms**、P95 **18.78ms**、最大 **19.76ms**；程序 RSS 峰值 **30.07 MiB**、FD 峰值 **51**、平均 **0.49 个 CPU 核** |
| AlmaLinux10 RPM / GPG / DNF4 | 普通用户 ops 的真实签名仓库同步成功；客户端由 sudo 在另一私有 installroot 安装 fixture 成功。未签名新版本被拒绝，原 repomd 和 RPM 正文可下载且不变；宿主默认 RPM 公钥列表前后相同。仅自建 noarch RPM，未启动完整发行版同步 |
| systemd 沙箱 | 临时单元使用 ops、LimitNOFILE=8192、UMask=0077、NoNewPrivileges、ProtectSystem=strict、ProtectHome、PrivateTmp 与独立 ReadWritePaths；启动、真实文件下载与正常停止均通过，退出码 0。测试路径/身份与正式部署不同，不冒充生产用户与挂载权限验证 |
| 低空间与 ENOSPC | 新建 4 GiB reflink XFS 镜像盘：不足准入量时 queued/waiting_space 且耗时 0，移除填充后自动执行；运行中跌破安全余量得到 DISK_SPACE，旧发布仍可读。仅在该镜像盘用 fallocate 实际观测 ENOSPC；没有填满 VM 根盘，没有将其冒充发布 fsync 故障。最终已卸载并删除镜像盘 |
| 中断与备份恢复 | 对隔离程序及其自有 rsync 子进程做 KILL，重启为 interrupted、继续成功；不是断电模拟。正常停止后备份完整 state、YAML 和镜像，并在原路径还原启动：2054 条记录、发布代 ID、正文 SHA 与通知密钥保留，退出码 0；未验证跨路径迁移或真实备份介质 |
| 退出与清理 | 全部测试程序/daemon 已退出，临时 systemd 单元停止，无遗留测试挂载/镜像盘；独立证据目录保留，见 latest/cleanup.json |

实际程序、驱动和原始证据归档为本机忽略文件 test-results/linux-opt-evidence-final.tar.gz，提取目录 test-results/linux-opt-evidence-final；脚本与产物在 test-results/linux-opt-bundle。远端隔离证据目录为 /var/tmp/mirrordock-acceptance-20261003-Hpcgju；systemd 因 PrivateTmp 会隐藏 /var/tmp，另建独立 /srv/mirrordock-acceptance-systemd-* 目录，未更改现有部署目录。

### 性能口径与对照

同一虚拟机交替测修复前与最终下载实现，8 MiB **稀疏文件、热缓存、回环 HTTP**、2s × 3；后续最后 SQL 补改未改变下载代码，保留已完成的性能轮次，未重复生成更好看的数值。

| 轮次 | 优化前 MB/s / allocs | 优化后 MB/s / allocs |
| --- | --- | --- |
| 1 | 6748.22 / 143 | 6973.53 / 121 |
| 2 | 6876.50 / 143 | 6872.09 / 121 |
| 3 | 6880.06 / 144 | 6693.02 / 122 |

每次分配约降低 **15%**，大文件吞吐均值基本持平，不能宣称显著吞吐提升。另用相同压力 fixture 对照日志清理，旧版本清理 2050 条历史时管理请求最大 **2375.98ms**（清理 2.37s），最终版本最大 **19.76ms**（清理 2.33s）；改善的是其他任务和管理请求被全局锁阻塞的问题，不把总删除耗时描述为大幅减少。压力客户端累计回环接收约 35.0 GB，仅为合成负载，非网卡吞吐或真实数据规模。

128 MiB **非稀疏文件**单客户端冷/热补验：仅对测试文件 POSIX_FADV_DONTNEED，以 mincore 复核 guest 缓存，暖时 32768/32768 页在驻，冷时 248/272/512 页在驻；未清空全系统缓存。最终热客户端 MB/s 为 **1739.93 / 1755.56 / 1747.03**，guest read_bytes 增量均为 0；冷 guest 缓存为 **1559.82 / 1576.64 / 1589.84**，增量 **133201920 / 133103616 / 132120576** 字节。所有正文 SHA-256 一致，单次约 0.08s，客户端哈希和新连接计入，宿主缓存/虚拟磁盘/物理网卡未控制，因此不是生产磁盘冷读能力结论。证据 latest/cache-results.txt。

带 strace 的 8 MiB HTTP/TLS 数字只用于协议和系统调用复核，包含新连接与 TLS 握手，不与上述无跟踪基准或冷文件混为一组。

**失败尝试保留说明：** 首次本机 sandbox 回环绑定受限未记通过，获准后重跑；验收 fixture 曾出现脚本命名遮蔽 Python http 包、限速配置层级错误、普通用户 daemon 显式 gid 导致 setgroups 失败、未知 rsync module 被正确视作配置错误、PrivateTmp 隐藏测试目录，以及驱动变量覆盖 round 内置函数。均修正测试脚本/路径后实际重跑，不据此修改产品配置协议或把失败当通过。外部 OCR 入口失败继续单独记录。

**仍未验证：** 4–6 TiB / 百万级文件与长期生产负载、真实物理网卡与宿主冷缓存、Linux race、SELinux Enforcing、EL8、最新 amd64 Docker 完整镜像构建/运行、真实通知渠道、真实断电/存储损坏/fsync 失败和生产备份介质。保留此前依赖审计结论，本轮没有新增依赖，也未重跑在线漏洞审计。历史原始日志与外部备份不自动改写，保持手动清理协议；若曾实际泄露凭据，应轮换并按部署记录处理备份残留，不能由本次隔离测试消除。

## 审查修复与最终回归（2026-10-03）

本次按用户授权修复全部七项已确认问题，保留工作区已有改动，未提交、推送、部署、启动真实发行版同步、发送外部消息或使用子代理。配置字段没有新增或恢复；明确收紧来源/GPG URL 的凭据边界，完整代理管理端回填例外继续保留。实现方案见 [修复计划](archive/plans/2026-10-03-review-fixes.md)。

| 问题 | 最终行为 / 永久回归 |
| --- | --- |
| R1 同任务并发 | resume 检查 working，queued 保存成功后才排队；worker 出队再次检查 working。运行中操作保存失败保留 pausing/running 和源预占。actions_test 覆盖暂停写库失败/旧执行器停止窗口、退出后正常恢复、恢复写库失败不启动 |
| R2 FD 累积 | 路由不常驻打开当前/历史根，请求前获取 FD 配额，根按需打开、取得文件后关闭；TCP 与文件请求按同一软限制分配预算。独立测试进程内软限制 128，150 次发布全部成功、保留 149 旧代，旧包回退成功；HEAD/GET 饱和均返回 503，活动引用/越界/Range/文件流现有测试继续通过 |
| R3 来源凭证快照 | 配置来源/GPG 地址拒绝 userinfo/query/fragment，GitHub 临时签名 URL 的通用下载校验不变；新快照防御性脱敏。历史 runs/run_results 以 100 条 keyset 分页迁移，相关候选/事件/错误 URL 同步脱敏，全部成功才写完成标记。105 条跨页两表保留记录与耗时、幂等、重叠地址、失败阻止启动及可重试均覆盖。迁移不改写 YAML、原始日志文件或外部备份 |
| R4 GitHub 无变化发布 | 更新私有标记后恢复父目录 mtime；无变化时仅保存新的发布摘要，不生成新正文代。受控 API 连续复用 received=0/skipped=1、代 ID 不变且摘要时间更新并持久化；随后资产身份/正文变化仍产生新代；摘要保存失败保留旧事实/work |
| R5 硬链接无变化发布 | dev/inode 分组，首个文件 reflink，其余仅链接同一 work 内的克隆。现代 rsync 连续无变化同步代 ID 不变；work 内 SameFile 为真，与 published 为假，写入 work 不改变发布正文 |
| R6 通知耗时 | 使用累计 ElapsedSeconds；执行 10s、墙钟 1h 显示 10s；旧记录缺失显示未记录，不猜测暂停耗时 |
| R7 通知未完成数 | 复用为成功子集，显示“其中复用”；未完成=总数-成功-失败。3 项中复用 2 项后失败正确显示 1 项未完成 |

15 个新增测试位于 source_url_test、actions_test、snapshot_test、review_test、descriptors_test、message_test；同时调整既有通知 fixture 的计数/累计耗时及旧根引用测试。此前七项临时复现保留在下方历史证据，不把其预期失败写成现版本测试失败。

| 本次检查 | 实际结果 / 证据 |
| --- | --- |
| Web format:check / build | 通过；review-fixes-web-{format,build}.txt；先构建 web/dist 再编译嵌入包 |
| 相关模块 Go race | config/database/runs/mirrors/notifications 通过；review-fixes-targeted.txt |
| 全套 Go race | 最终源码 8 个 internal 包全部通过，现代 rsync fixture 启用；review-fixes-go-race-final.txt |
| go vet | 最终源码通过；review-fixes-go-vet-final.txt |
| 本机 / Linux amd64、arm64 构建 | 最终源码三种构建均通过，临时二进制 /tmp/mirrordock-review-fixes-final 及两个 linux 架构产物；交叉构建不是 Linux 运行验收 |
| 全部 E2E | 最终二进制 11 passed、0 skipped（1.4m）；review-fixes-e2e-final.txt，首轮记录仍保留 |
| git diff --check / gofmt | 通过；无空白错误及未格式化改动 |

按 open-code-review 技能实际执行外部 review 入口，退出码 1：no valid LLM endpoint configured，外部模型调用未执行。随后使用项目允许的本地 delegate preview/rule，由当前 Codex 审查全部当前新增/修改源码，包括开始时已有改动。当前清单 44 项：25 个 reviewable 全部审查、0 skipped；默认排除的 11 个 Go 测试、2 个 E2E 与 6 个文档/计划另行核对。规则、清单与逐项结果见 test-results/review-fixes-{preview,rules,report}.json。审查补强了重叠 URL 必须先处理较长地址，以及迁移失败/重试用例；没有把外部入口失败或排除项当成自动通过。

**性能数据：** macOS arm64 / M2 Max 的 8 MiB 本机 HTTP fixture、1s × 3，修复前临时源码快照 6101/6539/6348 MB/s，当前 5463/5794/5891 MB/s；稳定轮次分配由 122 增至 134 allocs/op。按需打开 os.Root 增加路径解析/分配成本，以消除旧代句柄持续增长；两组分开执行，背景负载与短样本会影响差异，不作统计显著性结论。证据 review-fixes-download-{baseline,bench}.txt。这是热缓存、稀疏文件、本机 HTTP 合成基准，不能代表实际包磁盘读取、Linux sendfile 命中、HTTPS、冷缓存或生产多源吞吐。

**未执行项：** 本轮没有重跑 Linux 实机系统调用/吞吐、真实 EL/DNF/容器、外部通知、生产多 TB/大文件数与断电/磁盘满/fsync 故障，也没有最新在线依赖漏洞扫描。交叉构建只证明编译成功；历史验收证据保持原日期和边界。

## 全项目复查（2026-10-03，原始审查与复现）

范围为当前工作区全部项目模块，包括开始时已有的 25 个新增/修改文件。此次请求为审查，未修改业务源码、配置协议或依赖，未创建子代理、提交、推送、发布或发送外部消息。只更新本验收记录；证据在被忽略的 test-results。复现使用独立临时源码副本、临时数据、受控本机 GitHub API 和现代 rsync，不读取用户镜像或真实凭据。

使用 open-code-review 技能执行 `ocr review --audience agent --background ... --output /tmp/mirrordock-ocr-review-20261003.txt`，入口退出码 1，原因为 `no valid LLM endpoint configured`；没有执行外部模型调用。随后使用项目允许的 open-code-review-delegate 本地 preview/rule 工作流，由当前 Codex 审查，不宣称独立外部模型复核。Go 版本为 1.27.1，并读取 use-modern-go 的该版本完整指南。

为覆盖未修改源码，从 Git 跟踪文件及非忽略新增文件构造了临时源码快照，以空 Git 提交作为 OCR 基线。快照共 125 项，OCR reviewable 84 项全部记为 reviewed、0 skipped，覆盖率 100%；不是只审查工作区差异。覆盖清单、哈希、规则和逐项方法见 `test-results/whole-review-{manifest,preview,rules,report}.json`。

- 37 个 Go 生产文件：cmd、config、database、sync、runs、mirrors、notifications、observability、app，以及 web/embed.go；核对状态、调用关系、锁顺序、持久化、发布恢复、校验、凭证和文件服务边界。
- 12 个 TS/JS 文件：Vue 装配、API/状态/类型/格式、运行筛选与分页、构建/浏览器配置、公开目录脚本。
- 29 个默认规则文件：Vue 页面/组件/布局、HTML/CSS、嵌入图标、Dockerfile 和忽略配置；结合交互与模板边界、格式/构建、现有 E2E 核对，未做全部 CSS 像素或跨浏览器验收。
- 其余 6 个 reviewable：配置示例、两个维护脚本、两个 JSON 工具配置及 npm 清单。
- OCR 默认排除的 41 项另行核对：19 个 Go 测试、8 个 E2E、当前规则/文档/计划、systemd、Go/npm 依赖记录及图像资源。测试以结构与关键边界复核及实际全套执行验证；锁文件核对清单一致性，Go 模块执行离线校验；SVG 核对源与导入方式，ICO 仅核对生成入口和文件头，不做二进制文本审查。没有把工具排除当作自动通过。

### 已确认的问题

以下是修复前的复现记录，失败是当时的缺陷证据，不是通过记录；永久回归与修复结果见前节。

| 编号 / 优先级 | 位置与触发条件 | 已确认结果 | 修复方向 |
| --- | --- | --- | --- |
| R1 / P1 正确性 | internal/runs/runs.go:345–376；运行中的 pause 写库失败，原执行器正在停止时立即 resume | 写库失败把内存状态改为 interrupted，但 working 仍存在；resume 只检查状态，第二个 worker 再次运行同一 ID。SQLite 触发器拒绝 pausing、控制旧 runner 暂不退出，复现调用数 2 | resume 与 worker 出队均检查实际 working；旧执行器退出前不能重新排队，保存失败保留与执行器一致的状态和源预占 |
| R2 / P2 资源 | internal/mirrors/mirrors.go:165–181；24h 内持续发布新代 | 每个旧代永久打开 os.Root；安装新路由前还会同时打开旧、新两组根。仅测试进程把软 FD 限制设为 128，第 61 次发布、60 个旧代时失败 `too many open files`；没有活动下载 | 为发布根及下载连接共同计算 FD 预算；旧代可在保留 24h 与引用保护下按需打开或使用有界缓存，不以提高清理速度破坏旧包保护 |
| R3 / P2 安全 | internal/runs/runs.go:302–308，internal/config/config.go 的 HTTPURL；Reposync URL 使用查询鉴权 | `https://mirror.example/repo?access_token=REPOSITORY_SECRET` 被配置校验接受，凭证原样出现在 Snapshot.Source.Upstreams，并由 CreateRun 持久化到历史/API。此例只有虚构凭证；GPGKeys 也同样原样进入 Source | 明确来源 URL 的凭据边界，对来源/公钥地址做专门验证与历史脱敏；鉴权信息应在实际执行时读取。不能全局禁止 HTTPURL 查询参数，以免破坏 GitHub 签名资产 URL |
| R4 / P2 性能 | internal/sync/github.go:213–218，internal/mirrors/fingerprint.go:61–64；GitHub 所有资产均复用 | 每次 AtomicWrite 私有同步时间标记会修改父目录 mtime，指纹因此改变。受控 API 连续两次同步，第二次 received=0、skipped=1，但 Current.ID 改变 | 将同步时间更新与正文代的变化判定分离；保留正文身份、权限、链接等真正变化的检查，更新同步摘要不应强制生成新正文代 |
| R5 / P2 性能 | internal/mirrors/mirrors.go:345–363；默认保留硬链接、上游内容未变 | copyTree 逐文件克隆会拆散同一组硬链接；rsync --hard-links 再合并工作树，改变 inode/ctime。现代 rsync 连续两次处理同一隔离上游，第二次 transferred=0、created=0、deleted=0，仍产生新 Current.ID | 克隆时按源 dev/inode 保留工作树内的硬链接组；只对组内首个文件 reflink，其余链接到同一 work 内的克隆，禁止把 work 正文硬链接到 published |
| R6 / P2 正确性 | internal/notifications/message.go:54–59；任务暂停或服务中断后继续 | 通知用 EndedAt-StartedAt 计算“处理耗时”，与累计执行事实不一致。ElapsedSeconds=10、开始到完成相隔 1h 时显示 1h0m0s | 使用 ElapsedSeconds，并明确旧记录缺失时的展示；暂停时间不能进入累计执行耗时 |
| R7 / P2 正确性 | internal/notifications/message.go:61–63；GitHub 复用部分资产后失败 | GitHub Success 已包含 Skipped，通知又减一次 Skipped。3 个资产中复用 2 个、剩余 1 个未完成，显示“未完成：0” | 按执行器统一计数语义；复用为成功的子集，未完成不能重复扣减，可标注“成功中复用” |

R1 可在系统进程收到 TERM 后、最多 10 秒的退出窗口内发生；复现使用受控 runner 固定这个窗口。并发执行会共同使用同一 work/log/attempts，并覆盖 working 的所有权，因此需优先修复。R2 的 128 为隔离测试限制，不是生产阈值；实际阈值取决于 LimitNOFILE、源数量、发布频率和并发下载。R4/R5 会增加目录扫描、校验、fsync、旧代数量和 FD 压力，不表示每次都复制全部正文数据块。

### 本轮验证

| 检查 | 实际结果 / 证据 |
| --- | --- |
| `npm --prefix web run format:check`、`npm --prefix web run build` | 通过；先生成 web/dist 再检查嵌入 Go 包 |
| `go vet ./...`、本机程序构建 | 通过；test-results/whole-review-go-vet.txt；二进制 /tmp/mirrordock-whole-review |
| 现代 rsync fixture 启用的 `go test -race -count=1 ./...` | 8 个 internal 包通过；test-results/whole-review-go-race-local.txt。首次 sandbox 回环绑定失败保留在 whole-review-go-race.txt，不记作通过 |
| 完整 Playwright E2E | 11 passed，0 skipped；test-results/whole-review-e2e.txt |
| `GOPROXY=off GOSUMDB=off go mod verify`、npm 清单与锁文件核对 | 本地缓存模块校验全部通过，锁文件根清单字段一致；不是最新在线漏洞审计 |
| R1 / R4 / R5 / R6 / R7 复现 | 临时副本运行 `go test -race -count=1 -run '^TestWholeReview' ./internal/runs ./internal/mirrors ./internal/notifications`，5 个预期断言失败；whole-review-reproductions.txt |
| R3 复现 | 临时副本单项测试失败，打印虚构凭证进入快照的证据；whole-review-credential-snapshot.txt |
| R2 复现 | 测试进程内显式 Setrlimit 后，软限制 128，第 61 代发布失败；whole-review-descriptors-actual-limit.txt。初次只在 shell 设置软限制时 Go 启动恢复了限制，150 代通过的记录 whole-review-descriptors.txt 不能当成有效 FD 压力验证 |

修复前复现源码保存在 test-results/whole-review-reproduction-{runs,mirrors,notifications}.go.txt，仅作历史证据。修复阶段已按上述边界补齐永久测试；详细修复/审查报告见本文件前节与 review-fixes-report.json。

**进一步改进：** 七类场景已加入永久回归；大历史量的 ClearLogs 当前在整段扫描/删除/写库期间持有 Engine.mu，受控批次和短锁优化仍建议在大历史量实测后单独推进，此项未做生产规模耗时测量。当前模块边界总体可用，没有充分理由先做大规模架构重写。

**本轮未执行：** Linux 实机 sendfile/吞吐、真实 EL/DNF 与容器重新构建、外部通知、生产多 TB/大文件数规模、断电/磁盘满/fsync 故障、最新在线依赖漏洞扫描。下方此前验收证据继续作为历史记录，不能冒充本轮重跑；本轮也不证明所有逻辑边界均无问题。

## 镜像存储与系统设置（2026-10-03）

本次修改要求原生 reflink 克隆成功，禁止完整复制回退；未变化同步复用当前代，终态 work 自动回收，可恢复任务保留。rsync 去掉 delay-updates，并固定保护内部标记；实际 rsync 回归曾发现 --delete 删除准备标记导致无变化同步仍发布新代，已修复后重跑。安全余量为 max(1 GiB, 文件系统总量 1%)，任务准入另计每 worker 512 MiB；不足保持排队，不消耗预算，运行中每 5 秒低空间检查独立于清理循环。

系统设置 → 系统配置 → 环境检查新增 Reflink 和镜像盘空间。刷新在真实 published/work 目录重新实测，区分 supported/unsupported/error 并展示原因；容量使用 work 文件系统的 available/total、安全/下一任务准入余量，另列旧代和任务目录数，不将共享目录逻辑大小相加。删除配置源的留存文件不计入当前路由旧代数，但仍占实际文件系统容量。

| 检查 | 最新结果 | 证据位置（本机忽略文件） |
| --- | --- | --- |
| Web 格式与构建、Go vet、本机与 Linux amd64/arm64 构建 | 通过；先构建 Web，再编译嵌入包 | 本次工具执行结果、web/dist |
| 全量 Go race 与最后受影响模块复验 | 8 个 internal 包全部通过；最终 database/mirrors/runs 再次通过，真实 rsync fixture 启用 | test-results/storage-go-race.txt、storage-go-race-final.txt |
| 浏览器相关 E2E | 4 项通过；reflink 三种状态/原因/刷新、无变化 rsync、分页及公开目录；人工查看环境检查截图 | test-results/storage-e2e-final.txt、web/test-results |
| Linux arm64 / XFS reflink=1 | 最终 mirrors/runs 测试二进制原生运行，39 项顶层测试通过、0 skipped；原生克隆隔离、无变化、清理、准入/低空间、持久化失败覆盖 | test-results/storage-xfs-native-tests.txt、storage-xfs-filesystems.txt |
| 最终程序 XFS API 实测 | reflink=1 支持；reflink=0 不支持；published/work 跨文件系统不支持；实际 work 容量与 SIGTERM 退出码 0 | test-results/storage-xfs-api.json、storage-xfs-api.py |
| OCR 本地 delegate | 15 个 reviewable 全部 reviewed；默认排除的 5 个测试与 5 个文档另行完整核对；未遗留已确认的正确性/安全性/性能阻断项 | test-results/storage-review-{preview,rules,report}.json |

XFS 测试为独立 Colima VZ 虚拟机（2 CPU / 2 GiB、Ubuntu 24.04.4、Linux 6.8.0-117-generic），使用 4 GiB reflink=1 与 512 MiB reflink=0 的临时文件系统；不挂载宿主目录、不共享 SSH agent、不改变 Docker context，不运行真实发行版同步。验证后整机与临时磁盘已删除，用户 default 虚拟机保持 Stopped。先前传入二进制因 VM 临时目录权限失败，修正该临时目录后重传并通过；sandbox 回环绑定失败与外部 OCR 未配置 endpoint 的失败也未记作通过。

已按 open-code-review 技能执行外部审查入口，因没有可用 LLM endpoint 未执行外部模型调用；随后使用项目允许的 OCR delegate preview/rule 工作流，由当前 Codex 完整审查 20 个新增/修改源码（含测试），保留文件状态、覆盖原因与哈希，未向外部发送源码。审查修复包括：清理与容量监控分离、保存失败保留可恢复工作与源预占、`.gc-` 支持中断后继续、避免繁忙下载永久阻止回收、读取实际 work 文件系统容量、标记保护与基础代一致性检查。默认排除项没有当作自动通过。

**未验证项与性能边界：** 最新改动没有重做下面早期基线的 Docker/真实 Rocky DNF/下载吞吐验证；本次 Linux 原生覆盖限于 mirrors/runs 与环境探测 API，race 限于 macOS。4–6 TiB 生产 XFS 的全量同步、增量峰值、共享块物理占用、文件数规模下准备/指纹/回收耗时、同步下载并行的吞吐和断电恢复仍须目标服务器实测。容量保护不是硬配额，检查间隔内的突增或外部写入仍可导致 ENOSPC；新增/替换文件、旧代留存和文件系统元数据仍占空间。下载热路径未改动，未新增数据库读取、正文哈希或动态压缩。以下 preflight 证据保留为此前基线，不能冒充最新存储版本的重新验收。

## 环境与实际结果

macOS / darwin arm64，Apple M2 Max；Go **1.27.1**，Node **26.8.2**，npm **11.19.1**；测试 rsync **3.5.1**。所有运行状态、镜像 fixture、通知接收端和端口均隔离于临时目录/本机，不使用用户镜像或凭据。

| 检查 | 本次结果 | 证据位置（本机忽略文件） |
| --- | --- | --- |
| 冻结安装、Vue/TypeScript/Vite 构建 | 通过；真实 dist 嵌入 Go | test-results/preflight-web-build.txt、preflight-clean.txt |
| Prettier、gofmt、git whitespace、当前文档链接 | 通过；补修后再次检查 | test-results/preflight-format.txt、preflight-documentation-final.txt |
| go vet ./...、本机 Go 构建 | 通过，退出码 0 | 执行结果；preflight-clean.txt |
| 全量 Go race，count=1 | 8 个 internal 包全部通过；最终共享错误诊断调整后 sync 单包 race 再次通过，真实 rsync fixture 已显式启用 | test-results/preflight-go-final.txt、preflight-sync-final.txt |
| 浏览器回归 | 11 项当前行为通过；补修后全套 10 通过，第 7 项环境行使旧 6 行断言失败，更新断言后目标 1 项通过 | test-results/preflight-e2e-final.txt、preflight-e2e-settings-final.txt、web/playwright-report |
| 干净副本离线构建 | 无 node_modules/dist 的副本：npm ci/build、本机 Go、Linux amd64/arm64（CGO=0）通过 | test-results/preflight-clean.txt |
| 干净副本最小启动 | 默认配置、-f、--config；路由/全部嵌入资源/HEAD/认证/数据库/隔离/SIGTERM；参数冲突拒绝，全部通过 | test-results/preflight-smoke.txt |
| HTTP 文件基准 | 本机合成基准通过；数值见下 | test-results/preflight-benchmark.txt |
| Go 漏洞详细扫描 | govulncheck v1.8.0：darwin/arm64、linux/amd64、linux/arm64 均为可达漏洞 0、导入包漏洞 0；模块级 1 条为未使用的 openpgp | test-results/preflight-vulnerabilities-verbose.txt、preflight-vulnerabilities-linux-{amd64,arm64}.txt |
| npm 官方在线审计 | **完成**：完整锁文件（含开发依赖）102 个依赖，已知漏洞 0，退出码 0 | test-results/preflight-npm-audit.json |
| Linux arm64 实际下载 | 最终二进制在 Ubuntu 24.04.4 / Linux 6.8.0 隔离虚拟机通过；HTTP/1.1 观测到 38 次成功 sendfile，HTTPS 为 0；HTTP2、HEAD/ETag/Range/并发/退出通过 | test-results/preflight-linux-native-final.txt、preflight-linux-native-final-evidence.tar.gz |
| Linux arm64 后端测试 | 8 个 internal 包全部通过，0 skipped；GPG 补修后再运行受影响的 app/sync；真实 rsync 3.2.7 已启用；普通测试，race 结果仅为 macOS | test-results/preflight-linux-go.txt、preflight-linux-go-final.txt |
| Rocky 9 / DNF4 | 实际签名 RPM 同步、隔离客户端安装、未签名包拒绝/旧发布保留通过；普通用户同步和默认 RPM 密钥库不污染通过 | test-results/preflight-rocky-dnf-{after,nonroot}.txt |
| Docker 构建/运行 | Linux arm64 完整多阶段构建和默认入口启动通过；嵌入资源/认证/环境工具/文件请求/退出通过 | test-results/preflight-docker-build.txt、preflight-docker-runtime.txt |
| 容器系统安全更新 | 构建应用 Rocky 已发布安全更新；最终镜像已安装包待升级安全公告为 0，查询退出码 0，不等同于全 CVE 扫描 | test-results/preflight-docker-os-updates-{before,after}.txt、preflight-docker-runtime-final-versions.txt |

构建顺序始终先 Web 后 Go。普通 sandbox 首次 Go 测试因回环端口绑定受限失败，随后获准在隔离本地环境重跑并通过；首次 npm audit 因网络不可用失败。失败尝试未记为通过。离线 npm ci 的缓存 audit 摘要不能替代最新在线依赖审计。

主要命令（现代 rsync 与测试二进制路径按本机替换）：

```sh
npm --prefix web ci --offline --cache .cache/npm
npm --prefix web run format:check
npm --prefix web run build
GOCACHE="$PWD/.cache/go-build" go vet ./...
GOCACHE="$PWD/.cache/go-build" go build -trimpath -o /tmp/mirrordock-preflight ./cmd/mirrordock
GOCACHE="$PWD/.cache/go-build" MIRRORDOCK_TEST_RSYNC="$PWD/.cache/rsync/rsync-3.5.1/rsync" \
  go test -race -count=1 ./...
PATH="$PWD/.cache/rsync/rsync-3.5.1:$PATH" \
  MIRRORDOCK_TEST_RSYNC="$PWD/.cache/rsync/rsync-3.5.1/rsync" \
  MIRRORDOCK_TEST_BINARY=/tmp/mirrordock-preflight npm --prefix web run test:e2e
GOCACHE="$PWD/.cache/go-build" go test ./internal/mirrors \
  -run '^$' -bench BenchmarkPublicHTTP -benchmem -count=3
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o /tmp/mirrordock-linux-amd64 ./cmd/mirrordock
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o /tmp/mirrordock-linux-arm64 ./cmd/mirrordock
```

干净副本从当前源码复制，排除用户配置、状态、依赖与产物；依赖使用已有 npm/Go 缓存，Go 关闭网络代理/校验请求。交叉编译产物没有在 macOS 上运行，不能视作 Linux 运行验收。

## 修复与回归

| 已修复问题 | 行为与验证 |
| --- | --- |
| 两个 dest 可经符号链接指向同一/嵌套目录 | 同时校验规范路径与解析后目标；拒绝别名重叠，独立目录通过；config 回归 |
| 首次导入已存在镜像后，数据库提交失败留下私有孤立目录 | 提交失败恢复原目录；注入 SQLite 触发器拒绝发布，验证正文仍在且下一启动可导入 |
| 其他源历史挤掉安静源/旧活动任务状态 | 添加按源 id 索引与 latest API/SSE；125 条新历史之后仍保留旧暂停源；database 与 API 回归 |
| 已建立 SSE 在会话过期后继续推送 | 每次事件校验会话，终止失效流；读取第一事件后过期，验证随后 EOF |
| 退出或重叠刷新被晚到响应覆盖 | 取消刷新、会话/请求代际保护、清空状态；延迟配置响应后退出/重新登录 E2E |
| 详情只读取一次，排队转运行/备用尝试不更新 | 详情轮询、最新尝试跟随与历史选择保留；无尝试到运行、尝试 2/3、关闭停止请求 E2E |
| 日志读取成功可遮盖详情读取失败 | 分离详情/日志错误；无日志或已清理时禁用下载，避免打开无效导出 |
| 空备用来源隐藏后仍必填，切换 Reposync 无法保存 | 隐藏备用不必填，单来源规范写为 src1；真实 YAML 保存 E2E |
| 弹窗 Tab 焦点包含隐藏输入，初始 Shift+Tab 逃出 | 只包含可见控件，初始面板反向循环；恢复打开前 body overflow；键盘 E2E |
| 旧源编辑器携带新 revision 覆盖更新 | 保存打开时 revision，冲突由服务端拒绝；保留现有配置冲突测试 |
| 超大 srcN、非法 UID/GID 进入执行参数 | srcN 检查整数范围，UID/GID 为 0–4294967294；上下界回归 |
| cron 关闭未等待正在执行回调 | Close 等待 cron 停止完成，再收尾 worker；全量 race/退出回归 |
| DNF4 reposync 的私有 RPM 库无公钥，签名正确的包也失败 | 显式有界下载公钥、rpmkeys 导入私有 installroot，再运行 reposync；真实 Rocky root/普通用户、错误签名与旧发布保留回归 |
| Docker 仅安装工具，留下基础镜像旧系统包的安全更新 | 构建先执行 dnf upgrade --security，保留 RPM 签名校验，再查询最终已安装包的安全公告 |

同时检查非 JSON API 错误提示、通知页迟到响应、记录筛选请求取消与卸载清理。工作区原有“实际接收”累计与自然日/月时间范围改动已完整纳入回归，未知统计不伪造、夏令时昨天为 23 小时的边界测试通过。

## 依赖安全审计补验（2026-10-03）

npm 明确使用 `https://registry.npmjs.org` 官方服务，未过滤开发依赖，结果 info/low/moderate/high/critical 全部为 0。govulncheck 查询 Go 官方漏洞数据库，并分别以 darwin/arm64、linux/amd64、linux/arm64 分析 10 个根包、18 个模块和 Go 1.27.1 标准库；三种目标均未发现被导入包或可达调用的漏洞。

模块级记录为 [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932)：`golang.org/x/crypto/openpgp` 及其子包已经停止维护、所有版本均受影响，无已知修复版本。项目唯一 x/crypto 引用为 `internal/app/auth.go` 的 `bcrypt`；`go mod why golang.org/x/crypto/openpgp` 返回主模块不需要该包，源码引用检索与三个目标的分析一致。因此该记录为当前构建不适用，无需替换 bcrypt 或进行不能解决此告警的版本升级。保留原依赖版本及锁文件；不宣称模块级告警为零。

```sh
npm --prefix web audit --json --registry=https://registry.npmjs.org --cache="$PWD/.cache/npm"
GOCACHE="$PWD/.cache/go-build" .cache/tools/govulncheck -show verbose ./...
GOCACHE="$PWD/.cache/go-build" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 .cache/tools/govulncheck -show verbose ./...
GOCACHE="$PWD/.cache/go-build" GOOS=linux GOARCH=arm64 CGO_ENABLED=0 .cache/tools/govulncheck -show verbose ./...
GOPROXY=off GOSUMDB=off go mod why golang.org/x/crypto/openpgp
```

依赖审计没有要求升级应用依赖，清单与锁文件未再改变。随后真实 DNF 验证发现公钥导入缺失，已修复后重跑全量 race、受影响 Linux 测试与浏览器回归；三个 Go 平台扫描再次覆盖补修后的调用图。首次干净副本构建的记录保留，最终 Docker 从不含 node_modules/dist 的隔离源码上下文重新安装、构建 Web 与 Go，不把早期副本当作最终新增修复的构建证据。

## Linux 下载补验（2026-10-03）

在独立 Colima VZ 虚拟机中运行最终源码构建的 Linux arm64 二进制。分配 4 CPU / 4 GiB，Ubuntu 24.04.4 LTS、Linux 6.8.0-117-generic；未挂载宿主目录、未共享 SSH agent，未启动用户已有虚拟机。镜像正文为临时生成的 8 MiB 文件，上游禁用，端口仅在虚拟机回环监听；TLS 使用自签 fixture 证书，并由客户端显式信任。

最终二进制的 HTTP/1.1 与 HTTPS 都通过完整文件 SHA-256 比对、HEAD 无正文、ETag 304、Range 206 和越界 Range 416，以及 24 个请求 / 12 并发线程的范围正文比对。curl 实际协商 HTTP2 后校验完整文件；两个实例都在 SIGTERM 后退出码 0。strace 最终复核记录 HTTP/1.1 **38 次成功 sendfile**，HTTPS **0 次**，与 ADR-010 的实现边界一致；早期 40 次记录另外保留，不以系统调用次数的差异衡量性能。

另以 Go 1.27.1 在宿主离线交叉编译 8 个 internal 包的测试二进制（linux/arm64、CGO=0），将二进制和配置示例送入本地虚拟机执行 `-test.v -test.count=1`；每包退出码 0、0 skipped，真实 rsync 使用虚拟机的 `/usr/bin/rsync` 3.2.7。补修后重新编译并运行受影响的 app/sync。测试包含 Linux 构建路径中的进程/网络监测实现；此检查不带 race，不扩大此前 macOS race 的平台声明。

最终三次回环下载的 MB/s 为 HTTP 1558.10 / 1940.09 / 2264.12、HTTPS 140.17 / 173.68 / 166.29。这些数值在启用 strace 时测得，使用页缓存和每次新建连接；HTTPS 包含 TLS 握手，不能作为稳定连接吞吐或协议性能对比，也不能替代目标服务器的磁盘、网卡和冷缓存测量。测试脚本为 test-results/preflight-linux-native.py，系统调用与应用日志已归档到本机证据文件。

## Rocky、GPG 与容器补验（2026-10-03）

Rocky 9 aarch64、DNF4、RPM 4.16 的容器使用临时自建 noarch RPM 与临时 RSA 公钥，仅在容器回环提供仓库。工具安装完成后断开容器外网，不访问示例发行版或真实用户仓库。初次执行真实 reposync 时，正确签名包因私有 RPM 数据库没有公钥而被删除；这是命令 fixture 没有覆盖到的生产行为缺陷，失败日志保留在 preflight-rocky-dnf.txt。

修复以每个公钥 **1 MiB / 30 秒**上限下载，并继承任务上下文与累计预算；不输出公钥 URL 或网络原始错误。先用 `rpmkeys --root <任务私有 root> --import <私有公钥文件>` 导入，再执行 DNF4 reposync，结束删除私有目录。真实测试证明 root 和 UID 1000 都可同步签名仓库、拒绝未签名的新版本，并保留原先可下载发布；容器默认 RPM 密钥列表前后完全一致。客户端安装验证由 root 在另外的临时 installroot 执行，普通用户测试仅验证同步，不宣称非 root 安装系统包。

Docker 从干净源码上下文按 Node 24 → Go 1.27.1 → Rocky 9 的实际 Dockerfile 构建。用户指定的 [毫秒镜像](https://1ms.run/guide) 成功完成基础镜像下载；Node 首轮直连获取，Go/Rocky 使用加速入口，三个镜像摘要均与 Docker Hub 官方核对，记录于 preflight-docker-mirror.json。Go 使用 [Goproxy.cn](https://goproxy.cn/)，保留校验数据库；Rocky 工具安装使用 [中国科大软件源](https://mirrors.ustc.edu.cn/help/rocky.html)，完整构建遇到临时 429 后改用可达的南京大学 Rocky 源。Dockerfile 提供可选 GOPROXY/ROCKY_MIRROR 构建参数，默认协议未改变。

最终运行层先应用供应方安全更新，再安装工具、清理缓存。早期镜像的 OpenSSL/curl/glibc/Python 等旧包存在实际待安装安全公告，不能仅凭 npm/Go 无可达漏洞宣布整个容器无漏洞；最终 `dnf updateinfo list --security --updates` 返回空列表、退出码 0。OpenSSL 为 3.5.8、glibc 为 2.34-275.el9_8、curl-minimal 为 7.76.1-40.el9_8.7、Python 为 3.9.25-7.el9_8.3；完整版本与镜像 ID 见证据文件，不等同于独立容器 CVE 扫描。安全更新后再次运行 root/普通用户 DNF fixture，全部通过。

实际镜像使用默认入口与默认配置位置，测试容器无外网、无宿主挂载。认证/凭据不回显、rsync/dnf/rpmkeys 可用、Web 入口资源、完整文件/HEAD/ETag/Range 和 SIGTERM 退出码 0 验证通过。源文件、配置与实际镜像分别留有校验/执行证据；首次不支持的 buildx 参数、慢网络超时、软件源 429、测试客户端目录和同源请求头遗漏均按失败尝试保留或记录，未当作通过。

最终镜像导出为本机忽略文件 `.cache/mirrordock-preflight-image.tar.gz`（122,088,082 字节），可通过 `docker load -i .cache/mirrordock-preflight-image.tar.gz` 恢复标签 `mirrordock-preflight:current`。镜像 ID、归档 SHA-256 与体积见 test-results/preflight-docker-image.json；DNF 运行日志已另存 preflight-rocky-dnf-evidence.tar.gz，不包含临时私钥。验收后删除了本次独立 md 虚拟机、测试容器及专用临时目录；用户原有 default 虚拟机仍为 Stopped，Docker context 仍为 default，记录于 preflight-vm-cleanup.txt。

## UI、性能与代码组织

11 项 E2E 覆盖管理页面、真实 rsync 失败切换与发布、完整历史分页、通知本地接收/队列/帮助、分析请求竞争与错误/空态、日志跟随、源类型切换、会话竞争，以及公开目录搜索/排序/安全文件链接。视口包含 **320、375、736、1024、1440**；测试检查页面溢出、表格布局、分页、选项卡与键盘操作。分析/分页数据部分为受控响应 fixture，不能当作生产大规模数据库压测。

人工查看桌面源表、320px 日志与公开目录、375px 设置、320px Reposync 分析和桌面通知截图：当前未见遮挡、越界或控件无法操作。截图保存在 web/test-results 的各测试目录；不是跨浏览器像素基线验收。

页面路由改为按需加载，入口 JS 由 **214.26 KB → 125.97 KB（约减少 41%）**，最终本机构建器估算 gzip 为 76.88 KB → 49.62 KB；进入业务页面仍需相应路由/共享 chunk，不能把入口大小当全部资源。服务器不动态压缩下载，gzip 数值仅为构建器估算。

Vue/TS/CSS 和 E2E 使用固定 Prettier 3.6.2 与 format:check，拆开原来大量压缩到一行的源码；保留现有领域模块边界，共享开关与时间范围集中复用，未在发布前引入无必要的大规模后端拆分。机械格式化与功能差异分别核对，规范化后的前端差异报告为 preflight-semantic-diff.txt。目录、嵌入、依赖和执行入口一致；目录基线未为本次实现放宽。

本机 HTTP 8 MiB 合成下载基准（三次，macOS arm64，运行时存在其他回归负载）：

| 次数 | MB/s | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| 1 | 5513.48 | 1521473 | 11100 | 150 |
| 2 | 5365.54 | 1563423 | 9672 | 122 |
| 3 | 5565.24 | 1507321 | 9594 | 122 |

这些值反映本机页缓存/回环 HTTP，**不代表磁盘、生产网卡、Linux sendfile 或 HTTPS 吞吐**。源码复核保留真实文件、LimitedReader、ReadFrom 委托、有界缓存/并发及不可变发布引用；下载热路径未引入数据库、正文日志、全文件哈希或压缩。

## open-code-review 覆盖与结论

按照项目允许的本地 delegate 技能 `/Users/k/.codex/skills/open-code-review-delegate/SKILL.md` 的工作方式执行 OCR。命令为 `ocr delegate preview --format json` 与 `ocr delegate rule --format json <全部 reviewable 路径>`，当前 Codex 结合规则、完整新文件、差异及调用上下文审查。未执行外部 OCR LLM，未向外部发送源码；这是当前 Codex 的本地审查，不宣称独立外部模型复核。

最终 preview **82 个文件，45 个 reviewable**；45 全部 reviewed、0 skipped，覆盖率 100%。默认排除的 **37 项**另行检查：17 个测试、1 个锁文件、6 个当前文档、13 个已删除旧计划/预览/组件。报告位于 test-results/preflight-ocr-{preview,rules,review}.json，含路径、状态、摘要、排除原因和文件哈希。

45 个 OCR reviewable 文件覆盖清单：

- Go（9）：internal/app/{app,location}.go；internal/config/config.go；internal/database/database.go；internal/mirrors/mirrors.go；internal/runs/runs.go；internal/sync/{github,process,runner}.go。
- Web app（6）：web/src/app/{App.vue,api.ts,format.ts,router.ts,state.ts,types.ts}。
- 公共组件（8）：web/src/components/{AnalysisIcon,Modal,OptionCard,Pagination,SelectField,StackedTime,StatusBadge,ToastStack}.vue。
- 业务（13）：features/analytics/{Analytics,TrendChart}.vue；features/runs/{Batches,Records,RunDetail}.vue、useRunPage.ts、timeRanges.ts；features/sources/{SourceEditor,Sources}.vue；features/schedules/Schedules.vue；features/settings/{EnvironmentChecks,Notifications,Settings}.vue（均在 web/src 下）。
- 布局/样式/分页（3）：web/src/layouts/Shell.vue、styles/global.css、lib/pagination/usePagination.ts。
- 构建配置（5）：web/index.html、package.json、playwright.config.ts、vite.config.ts、.prettierrc.json。
- 容器构建（1）：Dockerfile，包含可选国内源、安全更新与最终嵌入/运行入口。

OCR 排除原因：default_path 18（测试与锁文件），unsupported_ext 17（当前及删除 Markdown），deleted 2（旧 OptionCard 与 HTML 预览）。删除项检查引用和替代入口；文档核对实际行为/命令，锁文件检查 Prettier 固定版本和清单一致。未把默认排除当作“自动通过”。额外复核启动、认证、配置/凭据、通知、数据库、任务预算/停止、GitHub 续传/重定向、镜像校验/发布、公开目录和文件根的边界。

上表正确性/安全性/性能问题已修复，最终审查未遗留已确认的阻断代码问题；相关格式、构建、Go race 和 E2E 回归已完成。环境和审计限制仍按下节处理。

## 上线环境待验

1. **生产规模与硬件**：用户 AlmaLinux 10.2 x86_64 目标 VM 已验证慢客户端、guest 冷/热缓存、同步下载并行的 CPU/RSS/FD/管理延迟及 sendfile；生产多 TB/百万文件、物理网卡、宿主冷缓存和长期负载仍待实测。Linux race 未执行，SELinux Enforcing 未验证。
2. **其他 EL / 完整仓库**：Rocky 9 aarch64 历史记录与当前 AlmaLinux 10.2 amd64 均完成受控 RPM/GPG、客户端安装验证；EL8、EL9 amd64 及目标仓库全量同步仍待部署环境验证。未运行示例完整发行版同步。
3. **容器与正式部署权限**：Linux arm64 镜像的历史构建/运行记录仍保留；目标 amd64 systemd 的普通用户和模板沙箱已通过。最新 amd64 容器完整镜像未构建/运行，正式服务身份、实际宿主挂载及证书权限未验证。
4. **真实通知与硬件故障**：本地受控通知接收端、目标机进程 KILL/恢复、临时盘低空间与 ENOSPC、原路径停机备份恢复已验证；未发送外部消息。正式渠道、真实断电/存储损坏/fsync 故障和生产备份介质仍需部署环境演练。
5. **已知模块告警处置**：openpgp 告警保留为不适用记录，项目未导入；未来引入相关包时必须重新评估，不把当前无可达调用结论沿用于新增功能。

此前自动审批拒绝追加依赖元数据发送。**2026-10-03 用户明确授权后，该阻塞已解除**，npm 官方审计与三个平台的 Go 官方漏洞详细扫描均实际执行完成。只发送审计所需依赖元数据，未发送源码或配置凭据；外部 OCR LLM 授权范围没有扩大。

## 2026-10-09：CLI 版本与标准 Go 构建

当前范围：cmd/mirrordock/version.go、version_test.go、scripts/build.sh，删除 scripts/version.sh，更新 README、AGENTS 和目录规范。构建改用 Go 内置模块版本；标准 tag、伪版本与 dirty 状态不再被旧脚本覆盖。显式链接注入优先，用于无 Git 的源码包与既有 Docker VERSION 参数。目录基线升为 1.1.0，新增 §8.1 并复核 §13 案例和 AGENTS 引用。旧版“直接构建只读提交 ID”的结论已被本节替代。

- 工具链实测 Go 1.27.2 darwin/arm64，并读取 use-modern-go 完整指南。`GOCACHE="$PWD/.cache/go-build" sh scripts/build.sh` 通过，包含 Vue 类型检查、Vite 构建和 Go 编译，产物 bin/mirrordock。
- 审查后执行 `GOCACHE="$PWD/.cache/go-build" go test -race -count=1 ./cmd/mirrordock`、同缓存配置的 `go vet ./cmd/mirrordock`，均通过；`sh -n scripts/build.sh` 与 `git diff --check` 通过。单元回归覆盖原有 CLI 解析及版本优先级、空元数据、`(devel)`、伪版本、dirty 和显式覆盖。
- 在临时目录内，用当前 version.go 与项目相同模块名/Go 版本建立独立 Git 仓库，执行真实 `go build`：11 个构建场景通过，包括无 Git、无 Git 注入、无 tag、忽略 `1.1`/`v1.2`、标准轻量 tag、dirty、覆盖版本仍保留 modified、tag 后提交的伪版本、注解预发布 tag、关闭 VCS 及关闭 VCS 后注入。二进制位于仓库外，测试结束删除临时仓库；未给项目仓库创建 tag。
- 在无配置的临时工作目录运行实际应用 `--version` 与 `--help`，均成功退出。当前仓库无 tag，本次版本为 `v0.0.0-20261009102508-0e2b7e8e744c+dirty (revision=0e2b7e8e744c)`。普通构建不需要手动注入，运行时无需 Git。
- OCR 外部入口 `ocr review --audience agent --background ... --output test-results/version-standard/ocr-external.txt` 退出码 1，原因为 `no valid LLM endpoint configured`，未执行外部模型审查、未发送源码。随后使用 open-code-review-delegate 的本地 preview/rule，由当前 Codex 审查全部变更；不宣称独立外部模型复核。
- OCR reviewable 的 version.go、build.sh 为 2/2 reviewed、0 skipped。默认排除的 version_test.go（default_path）、删除的 version.sh（deleted），以及 README、AGENTS、目录规范、本验收文档（unsupported_ext）另行完整复核，总覆盖 8/8。检查入口顺序、链接覆盖、dirty 去重、回退规则、删除脚本引用和文档一致性，无遗留已确认的正确性、安全性或性能阻断项。规则、覆盖清单、哈希、测试脚本与结果位于 `test-results/version-standard/`。
- 未执行：完整 Docker 镜像构建、Linux 运行验收、全套后端、Web 格式检查及浏览器 E2E（本次无业务/Web 源码变化）。无 Git 注入已在本机真实 Go 构建验证，不代替容器验收。本次无下载性能改动或性能结论；未运行真实同步，未修改用户镜像与凭证。

## 2026-10-17：运行详情日志工具栏

当前范围：web/src/features/runs/RunDetail.vue、run-detail.css、web/e2e/log-controls.spec.ts。刷新、跟随、下载三个按钮收进 `.log-actions` 整组右对齐，窄屏换行时整组移动而不拆散；跟随最新日志图标由 ArrowDownToLine 改为 ChevronsDown，避免与下载图标相同。

- `npm --prefix web run format:check`、Web 构建通过；以新构建嵌入 Web 的临时二进制运行 `e2e/log-controls.spec.ts` 通过。新增断言：桌面下载按钮右缘与日志框右缘对齐；375/320 宽度三按钮同一行且右缘对齐。修改前 320 宽度实测下载按钮单独换到左侧，已由整组容器修复。
- OCR 外部入口 `ocr review --audience agent --background ... --output <scratchpad>` 退出码 1，原因为 LLM 认证被拒（HTTP 401），未执行外部模型审查。改由当前代理人工复核全部 3 个变更文件（OCR 选中 RunDetail.vue、run-detail.css；E2E spec 为默认排除的测试路径），未发现正确性、安全性或性能问题；不宣称外部模型复核。
- 未执行：全套 E2E、Go 测试（本次无 Go 与下载路径改动）。

## 2026-10-17：rsync 增量递归阶段跳变

现象：同步 AlmaLinux 源时，运行阶段在“准备”和“传输”间反复切换。原因：`--info=progress2` 在增量递归时每完成一个文件输出带 `ir-chk=` 的行，旧解析把这类行判为 `scan`（前端归入准备），下一条普通进度行又回到 `transfer`。

当前范围：internal/sync/process.go、runner_test.go。进度行一律为 `transfer`；文件完成行（`-chk=`）的时间是已用时，不作 ETA；`ir-chk` 出现后至 `to-chk` 出现前文件列表仍在增长，隐藏 percent/total/ETA，避免数值闪烁；`to-chk` 后恢复百分比并沿用最近一次 ETA。尝试开始到首条进度行之前仍由 runs 设为 `scan`。

- `go vet ./internal/sync`、`go test -race -count=1 ./...` 全部通过。新增回归用用户日志的真实行序列，断言阶段始终为 transfer、增量期间不出现百分比/ETA，并覆盖 to-chk 完整列表下的百分比与 ETA。
- OCR 外部入口再次因 LLM 认证被拒（HTTP 401）退出码 1，未执行外部模型审查；由当前代理人工复核 process.go 与测试（ParseProgress 唯一调用方、双 goroutine 共享状态由既有互斥锁保护、reposync 输出不含 `-chk=`），未发现正确性、安全性或性能问题。
- 未执行：真实 AlmaLinux 同步验证（用户正在运行的任务使用旧二进制，需重新部署后观察）。

## 2026-10-17：任务控制二次确认与 OCR 托管审查约定

当前范围：web/src/app/state.ts、features/runs/Batches.vue、features/overview/Overview.vue、web/e2e/preflight.spec.ts、AGENTS.md。批次记录与概览中的暂停、继续、取消、重新同步统一在 `runAction` / `rerunSource` 内先 `window.confirm`（沿用删除同步源、清理日志的既有方式），取消则不发请求；提示写明任务名与后果（暂停保留工作目录可继续；取消不可继续、清理未发布工作目录、已发布镜像不受影响）。同步源/计划页的“立即同步”保持不确认。AGENTS.md 改为所有 OCR 审查一律使用托管模式。

- `npm --prefix web run format:check`、Web 构建通过；以新构建嵌入 Web 的临时二进制运行全部 E2E，13/13 通过。新增用例覆盖四种操作：取消确认时不发请求、确认后各发一次对应接口，提示含任务名。
- OCR 托管审查：`ocr delegate preview` 为 workspace 模式，reviewable 3/3 reviewed（state.ts、Overview.vue、Batches.vue），排除 AGENTS.md（unsupported_ext）与 preflight.spec.ts（default_path），二者人工完整复核，总覆盖 5/5。未调用外部 OCR LLM，不宣称外部模型复核。
- 审查发现并已修复：提示模板用 `String.replace` 代入任务名会解释 `$&` 等替换序列，改为模板函数；概览“重新同步”恢复以当前源 ID 与名称发起，避免显示旧快照名。修复后重新格式检查、构建并全量 E2E 通过。
- 未执行：Go 测试（本次无 Go 改动）。

## 2026-10-17：未完成同步目录复用

需求：大源同步到一半失败/超时/取消时，下次同步不必从上一发布代重新开始。用户确认：失败、超时、取消都保留；固定保留 7 天（不新增配置字段）；有任务因空间不足排队时，按结束时间从旧到新回收其他源的保留目录，排队源自己的不回收。

当前范围：internal/mirrors/keep.go（新增）、mirrors.go、storage.go、internal/database/database.go、internal/runs/runs.go、web/src/app/state.ts，测试 keep_test.go（新增）、mirrors/storage_test.go、runs/storage_test.go，README 与 DESIGN。

- 行为：已写准备标记的失败/超时/取消 work 保留，磁盘不足失败、源删除或类型/dest 变化、超过 7 天时清理；每源只留最新一份。同源新任务在自身 work 不存在时改名接手（同一文件系统，无复制），记录事件“复用任务 #N 保留的未完成同步目录”；暂停/中断续跑仍用自身目录。删除先改名为 `work/.discard-*` 再删，与接手共用互斥，残留由下一轮或启动清理完成。发布条件不变：仍需完整同步与校验，失败不替换公开代。
- `RunState` 改为返回状态列与原始文档；文档无法解码时仅视为不可复用，不阻断清理（审查发现，已补回归）。reposync 私有 DNF 目录在每次执行开始时清除，接手旧树不会沿用旧密钥或缓存。
- `go vet ./internal/...`、`go test -race -count=1 ./...` 全部通过。新增覆盖：三种终态均可被下一任务接手且为移动而非复制、续跑不重复接手；磁盘不足/过期/成功/旧份/源删除/改类型不保留；无排队时不回收、有排队时只回收最旧的其他源、容量探测失败不回收；文档损坏不阻断清理；引擎端取消后下一任务从保留目录开始并记录事件。
- 取消确认文案改为说明保留 7 天供下次复用。`npm --prefix web run format:check`、Web 构建通过，全量 E2E 13/13 通过（RunState 容错修复前执行；之后仅 Go 内部改动，已重跑全量 Go 测试）。
- OCR 托管审查：`ocr delegate preview` workspace 模式，reviewable 6/6 reviewed（database.go、keep.go、mirrors.go、storage.go、runs.go、state.ts）；排除 README.md、DESIGN.md（unsupported_ext）及三个测试文件（default_path），均人工完整复核，总覆盖 11/11。未调用外部 OCR LLM。
- 未执行：Linux 实机与真实大源验证（保留目录磁盘占用、超过 30 秒的大树删除跨轮继续），需部署后观察。

## 2026-10-18：确认改为页面内自绘模态窗口

当前范围：新增 web/src/components/ConfirmDialog.vue；修改 web/src/app/state.ts、layouts/Shell.vue、features/runs/Records.vue、features/sources/Sources.vue、web/e2e/preflight.spec.ts。暂停、继续、取消、重新同步，以及删除同步源、清理日志，全部从 `window.confirm` 改为 `confirmAction`：状态层保存唯一待决确认（新请求、登出、外壳卸载或切换路由时按“不确认”结束），外壳渲染 `ConfirmDialog`（`role="alertdialog"`、白底、按语义着色的图标与按钮）。危险操作初始聚焦安全按钮，Tab 只在两个按钮间循环，Escape 与点击遮罩视为取消；键盘监听在捕获阶段并阻止传播，不会顺带关闭下层 Modal 或移动端菜单。取消任务的返回按钮写“保留任务”，避免两个“取消”。

- `npm --prefix web run format:check`、Web 构建通过；以新构建嵌入 Web 的临时二进制运行 E2E：14 通过，2 跳过（mirrordock.spec、public-directory 首例需 `MIRRORDOCK_TEST_RSYNC`，本机无现代 rsync，未执行）。改写批次控制用例改为操作页面内对话框（取消/确认、Tab 循环、Escape 恢复焦点、点遮罩取消、全程无原生 dialog），新增清理日志用例。
- 在内置浏览器中以临时配置（隔离端口、临时目录）手动查看桌面与 375px 宽度下的清理日志确认框，布局正常。
- OCR 托管审查：`ocr delegate preview` 为 workspace 模式，reviewable 5/5 reviewed（state.ts、Records.vue、Sources.vue、Shell.vue、ConfirmDialog.vue），排除 preflight.spec.ts（default_path）并人工完整复核，总覆盖 6/6。未调用外部 OCR LLM，不宣称外部模型复核。
- 审查发现并已修复：待决确认会跨路由保留，导航后旧页面的确认仍显示；改为路由变化时按“不确认”结束。确认等待期间运行状态可能变化（原生 confirm 会阻塞页面，现不阻塞），确认后由服务端校验并以错误提示反馈，不另做处理。
- 未执行：删除同步源确认的 E2E（与清理日志同一路径，仅人工复核）；Go 测试（无 Go 改动）。

## 2026-10-18：手动运行状态回跳与批次进度条阶段显示

现象：同步源页手动运行后状态显示“运行中 → 已取消 → 运行中”；rsync 增量递归传输时批次进度条与“准备”阶段一样整条滑动。原因：点击后的 `refresh()` 直接取到新任务，而随后 SSE 推送的是点击前缓存（最长 1 秒）的快照，其中该源最新任务仍是上一条已取消任务；增量递归没有总体百分比，前端退回整条不定动画。

当前范围：internal/app/progress.go、app.go，测试 progress_test.go、app_test.go；web/src/app/state.ts、format.ts、features/runs/Batches.vue、RunDetail.vue、styles/global.css；README、DESIGN。

- 后端：已登录的非 GET 请求结束后使 SSE 快照失效（代数递增）并唤醒各连接立即推送；与失效重叠的读取不缓存、不返回，重新读取。匿名登录不触发失效（审查发现：否则未登录请求可迫使每次重新查询，已修复并补测试）。
- 前端：SSE 已连接时 `refresh()` 不再覆盖 `state.runs`，运行状态只来自有序的事件流；未连接时仍用接口结果。阶段映射移入 `format.ts` 由详情与批次共用；无百分比时进度条按准备/传输/校验/发布四段显示，已过阶段实心、当前阶段内动画，等待/重试阶段整条动画。有百分比时行为不变。
- `go vet ./internal/app`；`PATH=.cache/rsync/rsync-3.5.1:$PATH MIRRORDOCK_TEST_RSYNC=… go test -race -count=1 ./...` 全部通过（登录范围修复前执行；修复后重跑 `./internal/app` 通过）。新增覆盖：失效唤醒监听者、重叠读取被丢弃重读、失效后不复用缓存；匿名登录不失效、已登录写请求失效。
- `npm --prefix web run format:check`、Web 构建（含类型检查）通过；以本轮构建的临时二进制和现代 rsync 运行全量 E2E：16/16 通过。
- 在内置浏览器以静态页面加载 global.css 查看四种分段与百分比形态，显示正常。
- OCR 托管审查：`ocr delegate preview` workspace 模式，reviewable 7/7 reviewed（app.go、progress.go、format.ts、state.ts、Batches.vue、RunDetail.vue、global.css）；排除 progress_test.go（default_path），app_test.go 在预览后修改，均人工完整复核。未调用外部 OCR LLM。
- 未执行：真实大源 rsync 运行中的页面实测（状态回跳与进度条均未在真实同步中复现观察）；批次列表在 SSE 帧与 5 秒轮询记录间对同一任务仍可能有一秒内的短暂差异，未另做版本比对。

## 2026-10-18：开关选项改为开关前置的无边框行

当前范围：web/src/components/OptionCard.vue，features/settings/Settings.vue、Notifications.vue，features/sources/SourceEditor.vue。原两列卡片在窄栏里标题折行、高度不一，奇数项还会剩下单独一张卡。现在开关放在最左，后接标题和说明，不再显示 ON/OFF 文字，也不要卡片边框和开启底色。可点击区域收窄到开关和文字的实际宽度，键盘焦点框画在开关上。GitHub Release、Token、通知渠道改为单列；编辑同步源按用户要求保留两列（列间距 24px），480px 以下改为单列。组件名 `OptionCard` 未改，以免牵动调用方。

- 以临时配置（隔离端口、临时目录、生成的测试密码）启动本轮构建，用 Playwright 截取 GitHub Release、钉钉通知、编辑同步源三处 2 倍图（`test-results/option-card-preview/`，已忽略），供用户比较方案 B/C 后选定 C。
- `npm --prefix web run format:check`、Web 构建（含类型检查）通过。使用现代 rsync 3.5.1 运行 E2E：notifications、preflight、mirrordock 共 10/10 通过（编辑同步源恢复两列前的构建）；恢复两列后，以最终构建重跑 mirrordock.spec 和 preflight 中切换源类型的用例，2/2 通过。
- OCR 托管审查：`ocr delegate preview` 为 workspace 模式，reviewable 4/4 reviewed，无排除文件，规则为默认组。未发现正确性、安全或性能问题；SourceEditor 的最终网格改动在预览后做出，已人工复核。未调用外部 OCR LLM。
- 未执行：375px 窄屏的人工截图检查；其余 E2E（analytics、log-controls、pagination、public-directory、toasts）未涉及这些组件，本轮未跑；无 Go 改动，未跑 Go 测试。
