# 验收记录

## 概览进行中统计对齐与侧栏底部精简（2026-10-18）

范围：修改 `web/src/features/overview/Overview.vue`、`web/src/layouts/Shell.vue`、`web/src/styles/global.css`、DESIGN；测试 `web/e2e/overview-filter.spec.ts`。用户要求：首页“进行中”的实际接收/已运行/预算剩余/进度四项摊开；侧栏底部去掉“实时推送已连接”，只留“镜像目录”跳转。

- 统计：`.live-figures` 由 flex 换行（间距 32px）改为 4 等分网格，列间距与阶段条相同（6px），每项正好在“准备/传输/校验/发布”下方。`.live-run` 设为 inline-size 容器，内容宽度不足 420px 时改为两列，使“1023.9 GiB”等长值不折行。
- 侧栏：删除连接状态文字与圆点（含 forced-colors 下的样式），保留 `href="/"`、新标签打开的“镜像目录”链接，占整行并沿用原分隔线；`state.connected` 仍用于事件流与列表合并，不受影响。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` / `npm --prefix web run build`（含 `vue-tsc`） | 通过 |
| `overview-filter.spec.ts`（scratchpad 临时二进制，模拟一条传输中的批次） | 通过：1440px 下四项统计与四个阶段左边缘差 < 1.5px；375px 下四个值各占一行；侧栏底部链接文字为“镜像目录”、href 为 `/`。反向验证：去掉两列规则重建后该用例失败（“1023.9 GiB”折成两行） |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule` 选出 3/3 可审文件（通用规则组），由当前会话审查；排除的 `web/e2e/overview-filter.spec.ts`（default_path）已人工核对。发现 1 项：初版只按视口 ≤600px 改两列，而两栏布局下卡片最窄约 440px，四列时长字节值会折行；改为按卡片宽度的容器查询 |
| 未执行 | `mirrordock.spec.ts` 等依赖现代 rsync 的 E2E（本机仅有 openrsync，跳过）；其余 E2E 未全量重跑；Go 测试未运行（未改 Go 代码） |

## 分析页签、刷新与弹窗动效（2026-10-18）

范围：新增 `web/src/components/{RefreshButton,ModalTransition}.vue`、`web/src/lib/motion/tabIndicator.ts`；修改 `web/src/components/{Modal,ConfirmDialog}.vue`、`web/src/layouts/Shell.vue`、`web/src/styles/global.css`、`web/src/features/analytics/Analytics.vue`、`web/src/features/{overview/Overview,runs/Batches,runs/Records,runs/RunDetail,schedules/Schedules,settings/Settings,settings/EnvironmentChecks,settings/Notifications,sources/Sources}.vue`、DESIGN；测试 `web/e2e/shell-scroll.spec.ts`。用户要求：数据分析三个页签切换加线性动画，所有刷新给图标转一圈，按钮弹出的窗口加动画。

- 页签：`v-tab-indicator` 在页签栏上画一条下划线，按当前项的 `offsetLeft/offsetWidth` 定位，0.25s 线性滑动；首次定位后才启用过渡，避免从左侧扫入；MutationObserver/ResizeObserver 跟踪激活类与尺寸。用于分析、运行记录、系统设置三个页签栏，删除了原先每项各自展开的下划线样式。分析页内容按“出错/加载/数据”三态放入 `Transition name="page" mode="out-in"`，与侧栏换页相同：旧内容 0.12s 淡出、新内容 0.2s 淡入上移；数据在淡出期间到达时直接进入，不闪“加载中”；手动刷新保留旧数据原地更新。
- 刷新：`RefreshButton` 统一 11 处刷新（含“重新载入配置”，通知发送记录的图标由 RotateCcw 改为与其他一致的 RefreshCw）。点击的操作进行中或传入的 `spinning` 为真时旋转（0.7s/圈），至少一圈并在 `animationiteration` 时停在整圈；每次点击都执行操作（与原按钮一致，可中止卡住的请求再试）。运行详情与通知记录的定时轮询不旋转；日志按钮沿用读取期间旋转；减少动态效果时不旋转且立即复位。
- 弹窗：`Modal` 与 `ConfirmDialog` 去掉内部 Teleport，由调用处的 `ModalTransition`（Teleport 到 body + `Transition name="dialog"`）包住 v-if 的组件，使关闭也有动画：遮罩 0.2s 淡入、面板从下方 8px/98% 缩放落位；关闭 0.12s 淡出，过程中 `pointer-events: none`。仅遮罩根节点做 opacity 过渡，Vue 以其 transitionend 结束。确认框原有的入场关键帧改由同一过渡提供。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` / `npm --prefix web run build`（含 `vue-tsc`） | 通过 |
| E2E 全量（scratchpad 临时二进制，rsync 3.5.1） | 首轮 18 通过 1 失败：`shell-scroll.spec.ts` 在标题出现后立即断言内容列可滚动，分析内容现需多等一次 0.12s 淡出；改为 `expect.poll` 等待（断言不变）。审查修复后重跑全量 19 通过、0 跳过 |
| 本机时序采样（隔离端口临时实例，内置浏览器 + 无头 Chromium，逐帧读取计算样式） | 分析 Rsync→Reposync：下划线 0→104px 线性滑动约 250ms；内容 0.12s 淡出后 0.2s 淡入。刷新：本地请求瞬间完成仍转满一圈（约 700ms）后复位为 0°。新增同步源弹窗：挂在 body 下，0.2s 内 opacity 0→1、transform 由 `translateY(8px) scale(.98)` 到 none，焦点在面板；Esc 关闭 0.12s 淡出、期间 pointer-events 为 none，随后移除 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule` 选出 17/17 可审文件，由当前会话审查；排除的 `web/e2e/shell-scroll.spec.ts`（default_path）已人工核对。修复 2 项：`RefreshButton` 初版在操作挂起时忽略后续点击，网络卡住时无法像原按钮那样再点重试（改为每次点击都执行、按计数判断进行中）；删除已无使用方的逐项下划线与 `tab-indicator-in` 关键帧及页签项多余的 `position: relative` |
| 未执行 | 内置浏览器面板隐藏时 rAF 暂停，弹窗停在入场首帧，故弹窗时序改用无头 Chromium 采样；未逐帧目测确认框（`ConfirmDialog`）与运行详情弹窗，仅由同一过渡类与 E2E 覆盖；Linux 浏览器观感；Go 测试未运行（未改 Go 代码） |

## Rsync 下载代理：全局与按源跟随全局/直连/自定义（2026-10-18）

范围：`internal/config/config.go`（`settings.rsync.proxy`、源 `rsync.proxy_mode`/`rsync.proxy`、`ParseProxyField`）、新增 `internal/sync/connect.go`（`rsync-connect` 内部命令）、`internal/sync/{runner,process}.go`、`internal/runs/{runs,snapshot}.go`、`cmd/mirrordock/main.go`、`web/src/app/types.ts`、`web/src/features/settings/{Settings,EnvironmentChecks}.vue`、`web/src/features/sources/SourceEditor.vue`、`configs/config.example.yaml`、README/DESIGN；测试 `internal/config/config_test.go`、新增 `internal/runs/proxy_test.go`、`internal/sync/connect_test.go`、`internal/sync/{runner,strays}_test.go`（`command` 新增 env 参数、复用 daemon 启动辅助）、`web/e2e/{mirrordock,pagination}.spec.ts`。GitHub Release 与 Reposync 行为未改动。

- 配置：全局 `settings.rsync.proxy` 写法与 `settings.github.proxy` 相同；源 `rsync.proxy_mode` 省略/`global`/`direct`/`custom`，仅 custom 可填且必须填 `rsync.proxy`。页面：系统设置 Rsync 页“03 · 网络”；源编辑器“代理 · proxy_mode”三选一，跟随全局保存为省略（YAML 不新增键），切换离开自定义时清除地址。环境检查“下载代理”改名“GitHub 代理”，新增“Rsync 代理”行（全局状态与按源覆盖计数）。
- 执行：每次尝试从当前 YAML 解析代理；快照保留模式、清除地址；日志只写代理协议与主机端口，代理 URL 与凭据加入脱敏列表。rsync 经 `RSYNC_CONNECT_PROG` 启动 `mirrordock rsync-connect`（单引号转义路径，`%` 转义为 `%%`），代理/目标/连接超时经环境变量传递；rsync 环境始终剔除继承的 `RSYNC_PROXY`、`RSYNC_CONNECT_PROG`。**行为变化：此前 rsync 继承启动环境的 `RSYNC_PROXY`，现在只使用 YAML 中的代理。**
- 失败分类：代理不可达（可重试、`ProxyFailure`，本轮不切换备用来源，重试耗尽提示“代理不可用”）；认证/协议/配置错误（不可重试）；代理拒绝目标（HTTP 403、SOCKS 规则拒绝：不可重试的来源失败）；其他目标失败（可重试的来源失败）。隧道建立后的转发错误不再输出分类诊断，避免覆盖 rsync 自身判断（例如 unknown module 仍为不可重试的 `UPSTREAM_ERROR`）。

| 检查 | 结果 |
| --- | --- |
| `go vet ./...` / `go test -race -count=1 ./...`（`MIRRORDOCK_TEST_RSYNC=.cache/rsync/rsync-3.5.1/rsync`） | 通过 |
| 真实 rsync 3.5.1 经代理（`TestSystemRsyncThroughProxy`，本机合成） | 来源主机为只有代理能到达的 `mirror.invalid`：HTTP CONNECT 与 SOCKS5h（均带认证）完成传输；经代理访问不存在模块保持 `UPSTREAM_ERROR`；代理不可达得到可重试 `RSYNC_PROXY`，消息与日志不含凭据 |
| 内部命令与隧道单测 | http/https（自签根）/socks5/socks5h 隧道含 daemon 即时问候不丢字节；407/403/502/405/SOCKS 无可用认证/不可达代理/无响应代理超时分类；子进程中继与失败诊断；环境变量剔除与端口范围；含 `'`、`%`、`$()` 路径的 shell 引用 |
| 引擎（`TestRsyncProxyIsResolvedPerAttemptAndSkipsMirrors`） | 代理失败重试同一来源而不切换 src2；global/custom/direct/global 依次生效；快照不含地址；日志脱敏 |
| 配置（`TestRsyncProxyValidationAndResolution`、`TestRsyncProxyRoundTripsThroughYAML`） | 模式/地址组合校验、错误不含凭据、管理视图回填、写回保留注释且默认模式不写键 |
| `npm --prefix web run format:check` / `npm --prefix web run build`（含 `vue-tsc`） | 通过 |
| E2E 全量（scratchpad 临时二进制，rsync 3.5.1） | 19 通过、0 跳过。首轮 `pagination.spec.ts` 失败：其 mock 源缺少 `rsync` 块，新环境检查行访问 `source.rsync.proxy_mode` 抛错导致整块不渲染；改为可选链并将检查行数期望 10→11 后通过。`mirrordock.spec.ts` 新增全局代理保存、源编辑器自定义→直连→跟随全局及 YAML 断言，截图 `settings-rsync-proxy.png`、`source-proxy-custom.png` 已人工核对 |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build` | 通过（仅编译） |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule` 选出 12/12 可审文件（main.go、config.example.yaml、config.go、runs.go、snapshot.go、process.go、runner.go、connect.go、types.ts、EnvironmentChecks.vue、Settings.vue、SourceEditor.vue），由当前会话审查；排除的 README/DESIGN（unsupported_ext）与 7 个测试文件（default_path）已人工核对。修复 3 项：转发阶段错误带分类前缀会覆盖 rsync 自身诊断（改为不分类）；rsync 关闭后内部命令可能长期持有 stderr 管道（半关闭后 5 秒读取期限）；超出 65535 的端口在 SOCKS 请求中被截断（解析时拒绝）；诊断识别由包含改为前缀匹配 |
| 未执行 | Linux/EL 实机经真实 HTTP、HTTPS、SOCKS 代理同步；HTTPS 代理隧道下的真实 rsync 传输（仅单测覆盖隧道）；systemd 与容器内 `/bin/sh` 启动内部命令；运行中升级二进制后旧进程启动新版本内部命令的兼容性 |

## Rsync 剩余时间平滑与连续整体进度条（2026-10-18）

范围：`internal/sync/process.go`、`internal/sync/runner_test.go`、`web/src/features/runs/Batches.vue`、`web/src/styles/global.css`、`docs/DESIGN.md`；`web/e2e/mirrordock.spec.ts` 只把 favicon 期望从 `?v=2` 改为 `?v=3`（跟随 671208a 的有意改动）。配置协议与 API 字段未改动。

- Rsync 剩余时间 = rsync 报告的剩余字节（其 ETA × 其速率）÷ 最近 2 分钟平均处理速率；每秒最多一个采样，增量递归期间也采样但不显示估算，采样不足 30 秒显示“—”；文件完成行沿用上一次估算。
- 已核对 rsync 3.5.1 源码：`stats.total_transferred_size` 只在实际接收文件时累加（`receiver.c:976`），总量含全部文件（`flist.c:999`）。增量同步中未变化文件使剩余字节偏大、估算偏长，百分比在传输结束时也可能远低于 100%；窗口平均不能修正这一点。
- 增量递归期间不再隐藏百分比：保留 rsync 基于已知文件列表的近似值，封顶 99%（列表增长时可能回落），总量与剩余时间仍隐藏。批次列表去掉分段/滑动动画，始终是一条连续进度条；无百分比时准备/扫描/等待为空条，校验与发布为满条。进度条补充 `role="progressbar"` 与 aria 数值。

| 检查 | 结果 |
| --- | --- |
| `go vet ./...` / `go test -race -count=1 ./...`（`MIRRORDOCK_TEST_RSYNC=.cache/rsync/rsync-3.5.1/rsync`） | 通过；新增 `TestRsyncETAUsesWindowThroughput` 用假时钟覆盖增量期间隐藏估算、窗口平均替代瞬时速率、完成行沿用、过期采样丢弃与 30 秒下限；增量递归测试改为断言保留百分比且封顶 99 |
| `npm --prefix web run format:check` / `npm --prefix web run build` | 通过 |
| E2E 全量（scratchpad 临时二进制，rsync 3.5.1） | 19 通过、0 跳过。首轮曾因 `mirrordock.spec.ts` 的 favicon 期望仍为 `?v=2` 失败（671208a 遗留，此前因跳过未暴露），修正后通过；2026-10-18 并发设置记录中未执行的 E2E 步骤此次已执行 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule` 选出 3/3 可审文件（process.go、Batches.vue、global.css），由当前会话审查；排除的 ACCEPTANCE/DESIGN（unsupported_ext）、runner_test.go 与 mirrordock.spec.ts（default_path）已人工核对。未发现正确性、安全性或性能问题 |
| 未执行 | 运行中进度条无 E2E 断言与截图（运行中状态难以稳定构造）；真实长时间 rsync 下的估算与百分比效果；Linux 实机验证 |

## 概览时间轴按同步方式筛选（2026-10-18）

范围：`web/src/features/overview/Overview.vue`、新增 `web/e2e/overview-filter.spec.ts`、README 一句说明。后端与配置协议未改动。

- 时间轴卡片头在图例右侧新增下拉框：“全部类型 · N”及当前存在的各同步方式（Rsync / Reposync / GitHub Release，附源数量）；只有一种同步方式时不显示。选中后只显示该类型的行。
- 选择保存在浏览器 `localStorage`（键 `mirrordock.overview.type`），选“全部类型”时移除；存储不可用时照常工作。记住的类型已无源时回退到全部，但不清除记忆，配置加载前也不会被误清。
- 顶部 KPI、“下一次计划”、空状态与截断提示仍按全部源计算，不受筛选影响。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` / `npm --prefix web run build`（含 `vue-tsc`） | 通过 |
| E2E 全量（scratchpad 临时二进制） | 17 通过，2 跳过（`mirrordock.spec.ts`、`public-directory.spec.ts` 首例需 `MIRRORDOCK_TEST_RSYNC`，本机仅有 openrsync）；新增 `overview-filter.spec.ts` 覆盖选项计数、筛选、KPI 不变、刷新后记住、回到全部移除记忆、单一类型隐藏下拉与过期记忆回退 |
| 截图核对（时间轴卡片，1440 宽） | 下拉位于图例右侧，筛选后仅显示 3 行 GitHub Release；375 宽截图恰逢侧栏收起动画被遮挡，未作为结论 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule` 选出 1/1 可审文件（Overview.vue），由当前会话审查，未发现正确性、安全性或性能问题；排除的 `web/e2e/overview-filter.spec.ts`（default_path）已人工核对 |
| 未执行 | 窄屏无遮挡截图；需现代 rsync 的两例 E2E；Go 测试（无 Go 改动） |

## 同步任务按类型并发（2026-10-18）

范围：`internal/config/config.go`（新增 `settings.concurrency`）、`internal/runs/runs.go`（固定 2 个 worker 改为按类型调度）、`web/src/features/settings/Settings.vue`、`web/src/app/types.ts`、`configs/config.example.yaml`、README/DESIGN；测试 `internal/config/config_test.go`、新增 `internal/runs/concurrency_test.go`、`web/e2e/mirrordock.spec.ts`。

- `settings.concurrency.{rsync,reposync,github_release}` 各自 1–16 的整数，默认 4；缺省块或缺省键使用默认值，小数、0、17 与未知键被拒绝。页面“系统设置 → 同步策略 → 03 · 并发”编辑，保存经 revision 检查原子写回 YAML。
- 调度器按 FIFO 扫描队列，跳过所属类型已满的任务（保留队位），放行任务在独立 goroutine 执行，结束、配置保存或重载后立即唤醒。磁盘准入、公开下载繁忙检查仍对全部类型共用；每个活动任务仍预留 512 MiB。调度器在关闭开始后不再放行新任务。

| 检查 | 结果 |
| --- | --- |
| `go vet ./...` / `go test -race -count=1 ./...` | 通过 |
| `TestConcurrencyIsPerSourceType` | rsync 上限 1 时第二个 rsync 保持排队、GitHub Release 同时运行；改为 2 并保存后排队任务立即开始 |
| `npm --prefix web run format:check` / `npm --prefix web run build` | 通过 |
| E2E 全量（scratchpad 临时二进制） | 16 通过，2 跳过（`mirrordock.spec.ts`、`public-directory.spec.ts` 首例需 `MIRRORDOCK_TEST_RSYNC`，本机仅有 openrsync）；**新增的并发设置 E2E 步骤位于被跳过的 `mirrordock.spec.ts`，未执行** |
| 手工核对（临时实例，隔离端口与临时目录） | 同步策略页显示三项默认 4；改 GitHub Release 为 2 保存后 `/api/config` 与 YAML 均为 2；截图核对单列布局 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule` 选出 5/5 可审文件（config.example.yaml、config.go、runs.go、types.ts、Settings.vue），由当前会话审查；排除的 README/DESIGN（unsupported_ext）与三个测试文件（default_path）已人工核对。补强：关闭开始后调度器不再放行新任务；未发现其他正确性、安全性或性能问题 |
| 未执行 | 需现代 rsync 的两例 E2E；Linux 实机多类型并发同步 |

## GitHub Release 批量新增同步源（2026-10-18）

范围：`web/src/features/sources/SourceEditor.vue`、新增 `web/src/features/sources/GitHubBatch.vue`、`web/src/features/sources/githubBatch.ts`、`web/e2e/github-batch.spec.ts`、README 一句说明。后端与配置协议未改动。

- 仅在“新增同步源”且同步方式为 GitHub Release 时出现“批量添加”开关；编辑已有源不出现。开启后隐藏单个源的名称/唯一标识/dest/仓库输入，改为多行仓库输入（换行、空格或逗号分隔；接受 `owner/repo`、`github.com` 链接、`git@github.com:` 地址，去掉 `.git` 与 `?`/`#`，大小写不敏感去重；无法识别的条目单独提示并忽略）。
- 预览表每行可改唯一标识、源名称、dest、附件 glob（逗号分隔，留空沿用共用 glob）。id 依次尝试仓库名、`owner-repo`、`owner-repo-N`，取 id 与 dest 均不冲突的第一个；dest 为“前缀/slug”，前缀默认 `github`，只改动用户未手动编辑的 dest。前端按后端规则提示 id 格式/重复、名称为空、dest 非规范/保留/与已有源或其他行重复或嵌套；有错误时拒绝保存。
- Release 数量、共用 glob、Cron、启用状态共用；Cron 分钟字段为单个数字时按 `min(10, max(1, ⌊60/N⌋))` 分钟间隔递增（对 60 取模，小时等字段不变），否则全部使用同一 Cron 并提示。
- 保存时把全部新源追加到配置，经一次 `PUT /api/config`（revision 检查、原子写回）写入；后端校验失败则整批不写入。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` / `npm --prefix web run build`（含 `vue-tsc`） | 通过 |
| E2E 全量（scratchpad 临时二进制，修复前） | 16 通过，2 跳过（`mirrordock.spec.ts`、`public-directory.spec.ts` 首例需 `MIRRORDOCK_TEST_RSYNC`，本机仅有 openrsync） |
| E2E `github-batch.spec.ts` + `preflight.spec.ts`（审查修复后） | 9/9 通过 |
| 截图核对（1440 宽弹窗、375 宽） | 桌面预览表不再横向溢出；窄屏表格在 `.table-scroll` 内横向滚动 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule` 选出 3/3 可审文件（SourceEditor.vue、GitHubBatch.vue、githubBatch.ts），由当前会话审查；排除的 `web/e2e/github-batch.spec.ts`（default_path）已人工核对。发现并修复：`gitlab.com/a/b` 被误识别为仓库 `gitlab.com/a`（现拒绝含 `.` 的 owner）、GitHub 链接带查询串/片段无法识别、标题嵌套三元改为计算属性；均补入 E2E |
| 未执行 | 需现代 rsync 的两例 E2E；Go 测试（无 Go 改动）；真实 GitHub 同步（测试源均未运行） |

## 运行记录与计划管理表格居中（2026-10-18）

范围：`web/src/features/runs/Records.vue`、`web/src/features/schedules/Schedules.vue`、`web/src/styles/global.css`、`web/e2e/pagination.spec.ts`。

- 运行记录（成功、失败两个页签）参照同步源表改为 `table-layout: fixed` 固定列宽（`.records-table`，最小宽 980px，窄屏在 `.table-scroll` 内横向滚动）。同步源列固定 220px 并居中（名称过长时在列内换行；同日首版为居左，按用户要求改为居中）；时间、状态、耗时、数据量、详情居中；成功页签的“处理结果”居中，失败页签的“失败原因”保持居左。原因单元格依据该行有无 `error_code` 决定是否居中，去掉原 340px 上限，由剩余列宽承载。
- 计划管理：同步源列居左，Cron 表达式列改为居中，其余列原本已居中。计划管理同步源列去掉多余的 `text-left`（表格默认即左对齐）。
- E2E：`pagination.spec.ts` 新增计划管理表头/首行、运行记录成功页签表头/首行、失败页签表头的 `text-align` 断言。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` / `npm --prefix web run build`（含 `vue-tsc`） | 通过 |
| E2E `pagination.spec.ts`（scratchpad 临时二进制） | 1/1 通过 |
| 截图核对（`records-columns.png`，1440 宽） | 列宽与居中符合预期；截图恰在页面淡入过程中，颜色偏浅 |
| open-code-review | 未调用外部 LLM；`ocr delegate preview/rule` 选出 3/3 个可审文件（Records.vue、Schedules.vue、global.css），由当前会话审查；排除的 `e2e/pagination.spec.ts`（default_path）已人工核对。未发现正确性、安全性或性能问题 |
| 同步源列改居中后复验 | format:check、build、E2E `pagination.spec.ts` 1/1 通过；`ocr delegate preview` 仅选出 Records.vue（1/1 已审，排除 ACCEPTANCE.md 与 e2e 已人工核对），无问题；截图确认名称与类型行在列内居中 |
| 未执行 | 失败记录有数据时的截图、窄屏截图核对、其他 E2E |

本文件只记录**当前代码**的验证结果。更早的会话记录（含 2026-10-09 的 1.0 发布前验收、2026-10-16/17 的 AlmaLinux 10.2 实机验收）保留在 Git 历史中，查阅 `git show 7ca1573:docs/ACCEPTANCE.md`；那些结果是旧代码的事实，不能当作本次改动的验证结论，摘要见文末“历史实机验收摘要”。

## 1.0 上线前全量代码审查与修复（2026-10-18）

范围：基于提交 `7ca1573` 的完整仓库（Go 后端、Vue 前端、公开目录模板、数据库与连接池、构建与部署文件、依赖），本轮修复后的完整工作区。用户要求：全面审查前后端、数据库与连接池；代码分隔清晰、命名可读；依赖从源头核验真实性；隔绝常见安全漏洞；审查后直接修复并更新全部文档；项目未上线，不保留任何兼容代码或文档。

环境：macOS 15.8.1 arm64（本机合成测试，不代表 Linux 生产性能），Go 1.27.2，Node v26.12.0 / npm 11.20.0，测试 rsync 为 `.cache/rsync/rsync-3.5.1`。证据目录 `test-results/review-1.0/`（Git 忽略）。

### 依赖来源核验

| 检查 | 方法 | 结果 |
| --- | --- | --- |
| go.sum 与官方校验库 | 逐条对照 `https://sum.golang.org/lookup/<模块>@<版本>` | 34 个模块哈希全部一致 |
| Go 模块源头拉取 | 全新 `GOMODCACHE`、`GOPROXY=direct`、`GOSUMDB=sum.golang.org` 执行 `go mod download all` | 37 个模块直接从源仓库拉取并通过校验库，0 错误；本机默认 `GOPROXY` 为第三方 goproxy.cn，此步骤绕开了它 |
| 本机缓存一致性 | `go mod verify`、`go mod tidy -diff` | 通过；源头拉取时附带写入 go.sum 的 6 行无关校验和已还原 |
| `uuid` 导入 | `go doc uuid` | 为 Go 1.27 标准库包，不是无域名的第三方模块 |
| Go 已知漏洞 | `govulncheck` v1.9.0（proxy.golang.org） | 代码可达 0、导入包 0；模块级 GO-2026-5932（x/crypto/openpgp）项目未导入，判定不适用 |
| npm 锁文件来源 | 102 个包的 `resolved`/`integrity` 与 registry.npmjs.org 元数据逐一比对 | 全部一致，均来自官方 registry、sha512 |
| npm 签名与审计 | `npm audit signatures`、`npm audit` | 已安装 52 个包注册表签名通过，其中 32 个带来源证明；0 漏洞。含安装脚本的包仅 esbuild、fsevents（官方包） |
| 公开目录图标 | 用 `scripts/import-directory-icons.py` 从固定提交 `2b75f3a…`（phosphor-icons/core，2026-01-06）重新导入 | 与仓库内 `listing-icons.html` 逐字节一致 |
| 容器基础镜像 | Docker Hub API | `library/rockylinux` 自 2024-05-30 停止更新（问题见下表）；三个基础镜像 digest 已取得并固定 |

### 审查发现与处置

| 级别 | 类别 | 问题 | 处置 |
| --- | --- | --- | --- |
| 高 | 供应链 | 运行层 `FROM rockylinux:9` 是已停更的 Docker Library 镜像，基础层不再获得安全更新；三个基础镜像均未按 digest 固定 | 改用 Rocky 官方发布的 `rockylinux/rockylinux:9`；Node、Go、Rocky 镜像全部 `@sha256` 固定，Dockerfile 注明更新方法 |
| 中 | 供应链 | 容器构建 `npm ci` 执行依赖安装脚本；Go 阶段未显式校验 | `npm ci --ignore-scripts`（本机验证构建正常）；`GOSUMDB=sum.golang.org`、`GOFLAGS=-mod=readonly`，下载后 `go mod verify` |
| 中 | 安全 | 容器默认以 root 运行 | 新建 UID/GID 10001 的 `mirrordock` 用户并 `USER mirrordock`，预建目录 0750；README 说明卷权限 |
| 低 | 安全 | 构建参数 `ROCKY_MIRROR` 未校验即拼入 sed | 仅接受 `https://` 与 URL 安全字符 |
| 低 | 安全 | SQLite 的 `-wal`/`-shm` 文件按 umask 创建，可能对其他用户可读 | 打开后统一收紧为 0600，新增回归测试 |
| 中 | 数据库 | 无结构版本标记，未来升级无法识别旧库或防止旧程序打开新库 | 写入 `PRAGMA user_version = 1`，版本更高时拒绝打开；新增测试 |
| 低 | 可观测性 | 通知队列扫描失败只记“failed”，不含原因 | 日志附带错误（数据库错误，不含凭据） |
| 低 | 部署 | systemd 模板缺少常见内核保护项 | 增加 ProtectKernelModules/Tunables、ProtectControlGroups、LockPersonality、RestrictRealtime（未在 Linux 实机验证，见下） |
| — | 兼容遗留 | 启动清理旧版 `dnf-private-*` 目录；“旧版本管理员密码行”测试；Linux/macOS 以外平台的空实现（这些平台本就无法编译）；全局 CSS 中无人使用的“兼容别名”变量；README 中“旧二进制”“旧版本带鉴权地址”等说明；`docs/archive/plans` 历史计划 | 全部删除。`dnf-private-*` 现视为未知目录保留，测试已改为验证这一点 |
| — | 可读性 | 大量单行结构体字面量、长条件、长 SQL（最长 456 列）、250 行的 `config.Validate`、170 行的 worker 循环、12 个位置参数的下载函数、公开目录模板单行最长 1146 字符、顶层声明之间无空行、`e`/`st`/`o`/`g`/`b` 等含义不清的变量 | 见下节 |

审查确认**无需修改**的安全点（逐项核对源码）：SQL 全部参数化；rsync/dnf/rpmkeys 以参数数组执行、来源地址位于 `--` 之后，dnf 仓库文件的插值字段均经正则/URL 校验、不含换行；公开下载经 `os.Root` 限制（符号链接无法逃逸）、隐藏私有标记、`O_NONBLOCK` 防 FIFO、FD 配额、Range 段数上限、`sandbox` CSP；目录页 `html/template` 自动转义并以脚本哈希限定 CSP；管理写操作需自定义请求头且校验 Origin，会话 Cookie `HttpOnly`/`SameSite=Strict`/TLS 下 `Secure`，bcrypt 恒定比较、按连接 IP 限流且不信任转发头；GitHub API 重定向限同源、附件下载不带 Token、拒绝 HTTPS 降级、错误不回显上游正文；通知凭据 AES-256-GCM 并按渠道/字段绑定附加数据、禁止重定向、响应体限 64 KiB；前端无 `v-html`/`innerHTML`/`eval`，外链 `rel="noopener"`。

保留的设计边界（未改，已在 README/DESIGN 说明）：通用 Webhook 与 Bark 地址由管理员配置，可指向内网（管理员可信，非匿名 SSRF）；完整代理 URL 按已确认协议回填；管理台 CSP 保留 `style-src 'unsafe-inline'` 供 Vue 样式绑定；未下发 HSTS（同主机其他端口可能仍用 HTTP）；运行列表 `OFFSET` 由登录用户控制、受 5 秒查询期限约束。

### 可读性整改

- Go：所有非测试文件按字符宽度（中文计 2 列）检查，超过 120 列的行从 138 处（最长 456 列）降为 9 处，剩余均 ≤125 列（单个调用或带中文文案的错误构造，拆分反而降低可读性）。顶层声明之间补空行 132 处。
- 拆分：`config.Validate` → 服务/设置/路径/日志/TLS/源/执行器/计划各自的校验函数；`runs.Engine` 的启动恢复、清理循环、检查点、出队、空间等待、执行、结果映射、单次尝试、进度回调、发布、重试调度均为具名方法；`notifications.Service.Scan` 拆为同步、入队、`deliverNext`，SQL 全部提为命名常量；`sync.GitHubWithClient` 拆为列出 Release、选择附件、下载、写同步标记、清理旧版本，下载改为 `assetDownload` 结构体的 `run/open/copyBody`；`mirrors.reconcileSource` 拆出 `adoptRemoved`、`importDirectory`、`newGeneration`；`runs.Analyze` 拆出按日/按源/按错误三个查询函数，SQL 按子句分行。
- 魔数改为命名常量（会话与限流、下载槽位、分页大小、I/O 优先级编码、重试倍增上限等）；重复的通知渠道字段规则合并为 `config.ChannelFields.AllowedFor`。
- 公开目录模板按元素分行，行内内容保持单行（不引入可见空白，Go 精确子串测试全部通过）。
- 前端：Prettier（printWidth 100）全量通过，tsconfig.json 纳入格式化；长模板表达式提取为 `assetShares`、`storageDetail`、`describe` 等；单字母变量改为 `columnX`/`valueY`/`whole`/`size`/`distance`/`time` 等。剩余超长行只有通知渠道的中文帮助文案。

### 验证结果

| 检查 | 结果 |
| --- | --- |
| `gofmt -l`、`go vet ./...`、`GOOS=linux GOARCH=amd64 go vet ./...` | 通过 |
| `go test -race -count=1 ./...`（`MIRRORDOCK_TEST_RSYNC` 指向 rsync 3.5.1） | 9 个包通过；286 个测试（含子测试）通过，0 失败，**0 跳过**，无 data race。证据 `go-race.json` |
| 修复前基线（同命令） | 全部通过，用于对照 |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/mirrordock` | 通过 |
| `npm --prefix web run format:check`、`npm --prefix web run build`（含 vue-tsc） | 通过 |
| `npm ci --ignore-scripts` 后构建（临时副本） | 通过 |
| 浏览器 E2E（`MIRRORDOCK_TEST_BINARY` 为临时构建的二进制） | **17/17 通过**（约 2 分钟），含真实隔离 rsync 备用来源、发布、日志、分页、通知、会话竞态、公开目录布局/搜索/排序。证据 `e2e.txt` |
| Docker 镜像构建 | **未执行**：本机 Docker 守护进程未运行。新 Dockerfile（digest 固定、`--ignore-scripts`、`go mod verify`、非 root 用户、shadow-utils）需在 Linux 主机构建运行后才能确认 |
| systemd 新增加固项 | **未执行**：需在目标 EL 主机以服务身份验证 rsync/dnf 同步不受影响 |
| Linux 性能、sendfile、真实发行版同步 | **未执行**：本轮未改下载热路径的行为，只做了等价重构；结论仍以 Linux 实机重测为准 |

### OCR 托管审查

按项目约定只用 `ocr delegate preview/rule`（v1.13.1）选择文件与解析规则，由当前会话审查，没有调用外部 OCR LLM，不宣称外部模型复核。

覆盖：工作区模式，reviewable **43/43 reviewed、0 skipped、覆盖率 100%**（30 个 Go 源文件用 Go 规则组；Dockerfile、listing.html/css/js 与 Vue/CSS 用通用组；package.json、tsconfig.json 用各自规则）。OCR 默认排除 24 项，人工补审如下：

| 排除原因 | 文件 | 人工补审 |
| --- | --- | --- |
| default_path（测试） | 12 个 `*_test.go` | 逐一核对：删除旧密码行测试、清理目录测试改为保留 `dnf-private-*`、新增结构版本/权限测试、GitHub 下载调用改为 `assetDownload`；其余仅为顶层空行 |
| unsupported_ext | `deploy/mirrordock.service` | 新增 5 项沙箱选项，均不限制 rsync/dnf 所需的文件写入与网络 |
| unsupported_ext | README、DESIGN、IMPLEMENTATION、ACCEPTANCE | 核对命令与实际配置、链接与章节引用一致；删除兼容/历史说明 |
| deleted | `clone_other.go`、`space_other.go`、`docs/archive/plans/*`（5 份） | 确认无引用；非 Linux/Darwin 平台原本即因 `connections_*.go` 缺失无法编译 |

证据：`ocr-preview.json`、`ocr-rules.json`、`ocr-preview-final.json`、`ocr-report.json`。

审查结论：重构前后逐段对照了锁的进出约定（`Engine.run` 持 `e.mu` 进入、执行期间释放、收尾重新加锁）、取消与超时传递、资源关闭（响应体、文件、`sql.Rows`）、错误返回路径与原逻辑一致；未发现新增的正确性、安全或性能阻断项。审查中自查修正：`validate.go` 局部变量 `close` 遮蔽内建函数，已改名 `closeAll`。

## 上线环境待验

1. **容器**：在 Linux amd64/arm64 上构建新 Dockerfile，确认 digest 可拉取、`--ignore-scripts` 构建、非 root 用户对挂载卷的读写、rsync/DNF 同步与公开下载。
2. **systemd**：以服务用户启动新模板，确认同步、暂停/继续、停止与崩溃恢复不受新增保护项影响。
3. **生产规模与硬件**：多 TB/百万文件、物理网卡、HTTPS/HTTP2、长期运行、SELinux Enforcing、真实断电/存储故障。
4. **其他发行版与外部渠道**：EL8/EL9 全量仓库同步、真实通知渠道投递、经代理的 GitHub 完整下载。

## 历史实机验收摘要（旧代码，未在本轮重跑）

- 2026-10-09（1.0 发布前，AlmaLinux 10.2 x86_64 KVM）：Linux 全量 race、14 项 E2E、HTTP/1.1 sendfile 观测、受控 DNF4 reposync 与 GPG、磁盘不足、离线备份恢复、systemd 沙箱、真实 GitHub Release、Docker 多阶段构建均通过。
- 2026-10-16/17（AlmaLinux 10.2，XFS reflink）：reflink 共享块、暂停/继续、停机改 YAML、残留执行器进程清理（`TerminateStrays`）与 systemd 崩溃恢复通过。
- 2026-10-17/18 的界面改动（开关、提示横幅、侧栏固定等）各自运行过相关 E2E。

详细命令、数据与证据路径见 `git show 7ca1573:docs/ACCEPTANCE.md`。
