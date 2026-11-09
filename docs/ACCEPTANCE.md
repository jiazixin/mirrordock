# 验收记录

## CI race 测试中登录请求超时（2026-11-10）

范围：修改 `internal/app/auth.go`、`internal/app/helpers_test.go`。基线 `cc60198`，开始时工作区干净。

- 现象：CI run 42651015197 的 `go test -race` 中 `TestAdminAuthenticationWritebackAndPublicIsolation` 失败，`app_test.go:129` 的 `POST /api/auth/login` 超过测试客户端 5 秒超时；同一代码在上一轮 CI 通过，属计时性偶发失败。
- 原因：登录每次以 bcrypt cost 12 比对密码。本机实测单次比对普通模式约 0.23 秒、`-race` 下约 2.3 秒；`internal/app` 约 20 个并行测试启动时还各生成一次哈希，CI runner 核数少，单次请求可超过 5 秒。
- 修复：新增包级 `passwordHashCost`（生产值为 `bcryptCost` = 12，不变），`newAuth` 用它生成哈希；包内 `TestMain` 将其设为 `bcrypt.MinCost`。认证行为与 cost 无关，测试不再依赖 CPU 速度。未调大客户端超时。

| 检查 | 结果 |
| --- | --- |
| `gofmt` / `goimports@v0.50.0 -local mirrordock`（改动文件） | 通过 |
| `go vet ./...` / `staticcheck@v0.8.2 ./...` | 通过 |
| `CGO_ENABLED=1 go test -race -count=1 ./...` | 全部通过；`internal/app` 本机由 19.8 秒（改前）降至 3.5 秒 |
| `-race -count=3` 重复运行失败测试与 `TestSessionStoresOnlyDigestAndExpires` | 均通过，失败测试单次约 0.2 秒 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule`（v1.13.6）选出 1/1 可审文件（`auth.go`，Go 组），由当前会话审查，覆盖率 100%；排除的 `helpers_test.go`（default_path）已人工核对：包内唯一 `TestMain`，在任何测试启动前写入，无并发访问。无发现 |
| 未执行 | GitHub Actions 上的复跑（未推送）；`go vet` 各发布目标、`go test -tags integration`、govulncheck 未在本机运行（无依赖与平台代码变化） |

## 表单控件点击范围收紧到控件本身（2026-11-10）

范围：修改 `web/src/styles/global.css`、`web/src/components/OptionCard.vue`，新增 `web/e2e/click-targets.spec.ts`。基线 `b50f46a`，开始时工作区干净。

- 现象：新增同步源弹窗中，鼠标在下拉框、开关附近但不在其上时点击也会触发。原因是前端所有表单字段都用 `<label>` 包裹控件，label 比控件大，点击 label 任何位置都会转发给控件。实测：同步方式下拉的 label 为 372×96，框本身 372×42，标题文字、标题右侧空白、标题与框的间隙、框下方被网格拉伸出的约 26px 空白都会展开下拉；“启用此同步源”开关 36×20，label 156×50，开关上方 4px 内边距、开关下方空白、开关与文字的 12px 空隙都会切换。
- 全量排查：label 包裹写法出现在登录、同步源编辑、GitHub 批量添加、系统设置、通知设置、批次与运行记录筛选、搜索框共 44 处；开关统一由 `OptionCard` 渲染（同步源编辑、设置、通知共 11 处）。`RunDetail` 的 `<details>` 折叠标题是带分隔线的整行，属于可见的点击区域，未改动。
- 修复：全局 `label` 设为 `pointer-events: none`，其内的 `input` / `select` / `textarea` / `button` / `a` 恢复指针事件；label 仍提供可访问名称，键盘操作不受影响。`OptionCard` 的标题与说明文字恢复指针事件并收缩到文字宽度，点开关或其文字仍切换，空白不再触发。搜索框的放大镜图标叠在输入框上，点击穿透后仍聚焦输入框。
- 取舍：字段标题与提示文字（如“镜像地址：/…/”）不能再用鼠标选中复制。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` / `lint` / `typecheck` / `test` / `build` | 通过（单测 11 文件 30 项） |
| `click-targets.spec.ts` | 覆盖下拉标题/间隙/下方空白、文本框标题、开关内边距/空隙/下方空白/标题右侧均不触发，控件本身与开关文字仍生效；对改前代码运行失败、对新代码通过 |
| 其他页面核对（临时探针，未提交） | 批次页筛选标题不再展开下拉、点击框正常展开；同步源页点击搜索图标仍聚焦输入框；设置页逐个分类实测全部 14 个开关（GitHub Release 3 个、通知设置 11 个），开关上方 3px、下方 10px、开关与文字空隙、标题右侧均不触发，点开关与点标题均切换 |
| 触屏模拟（Chromium，`hasTouch` + `isMobile`，375×812） | 轻触下拉标题、文本框标题、开关下方 10px 不触发，轻触控件、选项与开关标题正常。离控件约 6px 以内的轻触仍会触发：普通按钮下方 3px、6px 轻触同样触发（鼠标不触发），10px 起不触发，属浏览器自带的触摸校正（把手指落点吸附到最近的可点元素），与 label 无关，未做覆盖 |
| Playwright E2E（按当前工作区重建 `bin/mirrordock`，`MIRRORDOCK_TEST_RSYNC=/opt/homebrew/bin/rsync`） | 全部 22 项通过 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule`（v1.13.6）选出 2/2 可审文件（通用组），由当前会话审查，覆盖率 100%；排除的 `click-targets.spec.ts`（default_path）已人工核对。无发现 |
| 未执行 | 真实手机（iOS Safari、Android Chrome）实机轻触；Go 测试（无 Go 改动） |

## 非 HTTPS 访问时立即同步失败、运行记录筛选栏重叠、清理日志改红色（2026-11-09）

范围：修改 `web/src/app/state.ts`、`web/src/components/SelectField.vue`、`web/src/features/runs/RecordsPage.vue`，新增 `web/src/app/state.test.ts`。基线 `0e0da4a`，开始时工作区干净。

- 同步源“立即同步”只提示“操作失败，请重试”、日志与批次均无记录：`runSource` 用 `crypto.randomUUID()` 生成 `Idempotency-Key`，该 API 只在安全上下文（HTTPS 或 localhost）存在；通过 `http://<局域网地址>` 访问时抛 `TypeError`，请求未发出，服务端自然没有任何记录，前端又把非 `ApiError` 统一显示为通用文案。改为 `crypto.getRandomValues` 生成 32 位十六进制键（128 位随机，符合服务端 `^[\x21-\x7e]{1,255}$`）；`perform` 遇到非 `ApiError` 时 `console.error` 原始异常，便于以后在浏览器控制台定位。
- 运行记录筛选栏重叠：`SelectField` 的 scoped `.select-field { width: 100% }` 与全局 `.filter-toolbar .select-field { width: 180px }` 特异性相同，组件 CSS 随懒加载 chunk 后注入而胜出；运行记录页两个下拉直接放在工具栏里，各被拉到约 1014px，搜索框被压成 0 宽（截图中双重左边框），时间下拉被挤出可视区。组件默认样式改为 `:where(.select-field)`（特异性 0），容器规则恒定生效；同步源、批次、分析、编辑弹窗、分页的下拉宽度逐一核对不变。
- 清理日志按钮由 `tone-amber` 改为 `tone-red`，与确认弹窗的红色一致。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` / `lint` / `typecheck` / `test` / `build` | 通过（单测 11 文件 30 项） |
| `state.test.ts` | 去掉 `crypto.randomUUID` 后仍发出 32 位十六进制 `Idempotency-Key`；对旧代码运行失败、对新代码通过 |
| 浏览器核对 | scratchpad 临时配置、127.0.0.1 隔离端口启动临时二进制：1440px 与 375px 下运行记录筛选栏显示搜索框、两个下拉与计数，清理日志为红色；同步源、批次、分析、编辑弹窗、分页的下拉宽度与改前一致；点击立即同步成功入队 |
| Playwright E2E（`MIRRORDOCK_TEST_BINARY` 指向按当前工作区新构建的 scratchpad 二进制，`MIRRORDOCK_TEST_RSYNC=/opt/homebrew/bin/rsync` 3.5.2） | 全部 21 项通过：无 rsync 时 19 通过、2 跳过，再以 rsync 运行 `mirrordock.spec.ts`、`public-directory.spec.ts` 3 项通过。注意：首次未设 `MIRRORDOCK_TEST_BINARY` 时默认使用 10-27 构建的旧 `bin/mirrordock`，`analytics.spec.ts` 耗时轴显示 `NaNhNaNm` 失败，未改动的 HEAD 用同一旧二进制同样失败；换新二进制后通过，属旧产物问题，`bin/` 未改动 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule`（v1.13.3）选出 3/3 可审文件（JS/TS 组 1、通用组 2），由当前会话审查，覆盖率 100%；排除的 `state.test.ts`（default_path）已人工核对。无发现 |
| 未执行 | 未在真实局域网 HTTP 地址的浏览器中复现（本机验证均为安全上下文 127.0.0.1，非安全上下文由单测模拟）；用户部署实例需重新构建发布后生效 |

## 全仓规范对齐与最终交付（2026-10-30）

本节是本轮交付依据，下方更早记录仅代表当时版本。审查基线为 `f6c1983208d17dbefe95951cc56cbb6d4af1c0ed`，包含开始工作时已有的相关未提交改动。按目录规范、通用代码规范、Go / Web / Vue 规范逐项核查；用户确认的 YAML 配置协议与 README 中已明确的产品例外优先，未改写规范以迁就实现。未创建子代理、调用外部 OCR LLM、推送或发布。

### 已完成的整改

| 范围 | 最终行为与复核重点 |
| --- | --- |
| 目录与工具链 | `mirror` / `run` / `notification` / `syncer` 使用单数包名；删除旧导入、重复 API 和旧浏览器兼容分支。Go 1.27.3，JSON v2，YAML `go.yaml.in/yaml/v3`；Go import 三组、选项参数、包和导出注释、`web/embed.go` 的 `all:dist`、`go.mod` 的 node_modules 排除均对齐 |
| 生命周期与取消 | 入口拥有信号上下文；先监听再 Start 后台工作；网络、数据库、命令和长文件操作传递 ctx；WaitGroup.Go 管理后台工作，关闭有界且等待退出；进程取消回调等待完成，代理 TLS 握手取消不再捕获可变连接变量，超时分类稳定 |
| 数据与接口 | SQLite 连接私有、单写四读、5 秒操作界限、统一写事务；任务与幂等首次成功结果原子落库，24 小时重试保持原始响应；UTC 固定三位毫秒，`elapsed_ms` / `budget_ms` / `eta_ms` / `delay_ms` 为整数；严格对象解码、拒绝未知字段、ETag/If-Match、分页元信息、204 与错误码约定一致 |
| 安全与资源 | 会话只保存 Token 摘要，限定数量和有效期；配置、运行快照、通知与任务格式化脱敏；GitHub 带认证 API 禁止重定向、附件重定向不携带认证；元数据、身份 sidecar、通知密钥、进程命令行读取有界；密钥拒绝符号链接、非普通文件与宽松权限；重要写入/回滚/关闭失败不伪装成功 |
| 下载与同步 | 保留受限文件根、不可变发布代、文件流与 HTTP/1.1 原始 ResponseWriter；下载不查询数据库、不逐块记日志、不整文件哈希、不动态压缩；同步资产校验所需哈希不在公开下载热路径。发布回滚、残留进程、暂停/重试累计预算、磁盘等待均覆盖测试 |
| Web / Vue | 应用状态只导出 readonly 与修改函数；所有请求走对象参数和显式响应类型；页面请求与会话请求分别取消，旧请求不能覆盖新数据；错误保留已有数据并提供重试，字段错误就地显示；筛选/分页/选中项进入 URL；共用弹窗、焦点回收、Esc 与下拉框顺序、scoped 样式、集中 token、统一格式化、无嵌套三元与非空断言 |
| 测试与门禁 | 可隔离的 Go 测试并行，环境/工作目录/进程限制测试串行；文件/网络/进程测试使用临时目录、隔离端口、可控上游与有界上下文；时间测试注入时钟，纯内存超时使用 synctest；补齐 Fuzz 语料、请求取消、错误保持、弹窗与 URL 回归；CI 按规范顺序执行固定版本检查，先全量对齐提交、再接入 CI |

未保留旧接口路径、旧 JSON 字段、旧包别名或 schema 迁移。数据库结构版本为 2，其他非零版本明确拒绝打开；没有删除或转换用户数据。保留旧发布代、失败工作目录和 GitHub 历史 Release 的产品功能不属于兼容代码。配置来源、相对路径、公开下载路径、管理路径和完整代理 URL 回填均遵循用户已确认协议。

### 最终验证

环境：macOS / darwin arm64，Apple M2 Max；Go 1.27.3、Node 26.13.0；受控 rsync 3.5.1。所有需要嵌入 Web 的 Go 检查前已构建 `web/dist/`。原始本地证据集中在忽略目录 `test-results/compliance/`，结论、覆盖与限制保存在本节。

| 检查 | 最终结果与证据 |
| --- | --- |
| Go mod verify / tidy -diff、gofmt、goimports v0.50.0 | 全部通过，`go-delivery.json` 与对应 `*-delivery.log` |
| go vet、integration/scale 标签 vet、staticcheck v0.8.2 | 全部通过；模板 AST 检查无多行复杂表达式/嵌套三元，TS 函数参数检查无超过三参函数 |
| `CGO_ENABLED=1 go test -race -count=1 -json ./...` | **418 个测试（含子测试）通过，0 失败，0 测试跳过，无 data race**；10 个测试包通过，`web` 仅因无测试文件显示包级 skip。`MIRRORDOCK_TEST_RSYNC` 指向受控 rsync；`race-delivery.log` |
| `go test -tags integration -count=1 ./cmd/mirrordock` | 通过，包含真实临时仓库构建的版本协议；`integration-delivery.log` |
| 8 个发布目标的 vet + CGO_ENABLED=0 build | 全部通过：linux amd64/arm64/ppc64le/s390x/riscv64/loong64，darwin amd64/arm64；`cross-delivery.json`。交叉编译不等于目标机器运行验收 |
| 4 个 Fuzz 入口，各 3 秒、GOMAXPROCS=2 | 全部通过：UTC 时间、代理 URL、请求 JSON、执行器进度；`fuzz-delivery.json`。短时模糊测试不代表穷尽输入 |
| Web format:check / ESLint + Stylelint / vue-tsc / build | 全部通过；`web-delivery.json` |
| Vitest | **29/29 通过，10 个测试文件**；`web-unit-delivery.log` |
| Chromium E2E | **21/21 通过**，包含真实隔离 rsync、配置与错误、会话竞态、暂停/继续/清理确认、分页/筛选、分析、通知、公开目录、移动布局、axe 严重可访问性问题检查；`e2e-delivery.log`，约 2.4 分钟 |
| govulncheck v1.9.0 | 可达漏洞 0，已导入包漏洞 0；模块扫描另提示 `GO-2026-5932`（x/crypto 的未维护 OpenPGP）。项目不导入 openpgp，仅使用该模块的 bcrypt；没有把模块级提示写成零。`govulncheck-delivery.log` / `govulncheck-detail.log` |
| npm audit --omit=dev | 生产依赖漏洞 0；`npm-audit-delivery.json` |
| actionlint v1.7.12（未启用 ShellCheck） | 工作流语法检查通过，`actionlint-delivery.log`；未触发 GitHub Actions 远端运行 |
| git diff --check | 通过 |

修复期间的失败保留为失败记录：旧 E2E 构建/断言、会话初始化被页面取消、弹窗焦点与 Esc、URL 持久化断言、Linux 专属调用遗漏、并行测试的过短 I/O 预算、代理取消错误分类、分析 benchmark 的旧时间夹具。均修正后执行相关验证，以上表格只引用最后通过结果；中途未指定 rsync 环境变量造成的跳过不计入最终测试结果。

### 本机合成性能样本

命令：`go test ./internal/mirror ./internal/run ./internal/database -run '^$' -bench '^(BenchmarkPublicHTTP|BenchmarkDirectoryListing1000|BenchmarkAnalytics5000Runs|BenchmarkLatestRuns|BenchmarkNotificationResultCursor)$' -benchmem -benchtime=300ms -count=1`，全部通过，证据 `bench-delivery.log`。

| 用例 | 本次结果 |
| --- | --- |
| 8 MiB 本机 HTTP 下载 | 1.77 ms/op，14,373 B/op，209 allocs/op；约 4,736 MB/s 为 loopback/cache 合成吞吐，不能代表物理网络或 Linux sendfile |
| 1,000 项目录渲染 | 16.60 ms/op，约 8.84 MB/op |
| 5,000 条运行记录分析 | 896.61 ms/op，约 2.58 MB/op；仅一次计时迭代，不能据此推断生产 P95 |
| 1,000 源最新状态 | 批量查询 1.48 ms/op；逐源查询对照 12.61 ms/op |
| 通知结果读取 | 插入游标 9.39 µs/op；历史反连接对照 112.55 ms/op |

这些数值来自本机短时合成运行，期间还有受控浏览器测试，不是隔离性能实验或生产容量保证；没有据此宣称 Linux 实机性能提升或 HTTPS sendfile。

### OCR 托管审查与覆盖

使用 `open-code-review-delegate`、OCR v1.13.3：本地 `ocr delegate preview/rule --format json` 选择文件、解析规则，由当前代理审查源码和差异；没有运行 `ocr review`，没有外部模型复核。规则组为 CI 1、Go 63、通用 31、JS/TS 28、package.json 1、JSON 1。

工作区预览 **238 项，125 项可审全部 reviewed，0 skipped，覆盖率 100%**（只指 OCR 可审清单，不等于正确性证明）。另人工补审 113 项排除内容：`default_path` 91（63 个 Go 测试、10 个 Web 单测、13 个 E2E 文件、4 份 Fuzz 语料、package-lock），`unsupported_ext` 16（规范、说明、模块和检查配置），`deleted` 6（迁移后的旧进程平台文件、旧格式化/弹窗/批次入口、已内聚的详情 CSS）。删除项均核对替代实现与引用，未通过删除测试规避失败。

全量源码盘点覆盖 `cmd/`、`internal/`、`web/src/`、`web/e2e/` 共 198 个现存源码/模板/样式/测试文件，此外核对 `web/embed.go` 与构建配置。未改动的 `cmd/mirrordock/version.go`、`ModalTransition.vue`、`timeRanges.ts`、`web/src/main.ts` 也读取核对：版本元数据遵循目录基线 8.1、弹窗过渡与统一弹窗配合、时间筛选枚举与后端一致、入口仅装配根组件/路由/共享样式。

审查修复重点：生命周期与锁的持有顺序、取消后的写入边界、身份与凭据处理、GitHub 认证重定向、流式下载资源边界、累计预算、原子幂等响应、UTC 规范化、只读状态与旧请求竞态、键盘/焦点与错误可恢复性。最终未发现尚未处理的正确性、安全、性能或规范阻断项。

<details>
<summary>OCR 可审清单（每项按 path + status 标识，均 reviewed）</summary>

| 文件 | 状态 | 结果 |
| --- | --- | --- |
| `.github/workflows/ci.yml` | modified | reviewed |
| `cmd/mirrordock/cli.go` | modified | reviewed |
| `cmd/mirrordock/log.go` | added | reviewed |
| `cmd/mirrordock/main.go` | modified | reviewed |
| `internal/app/analytics.go` | modified | reviewed |
| `internal/app/app.go` | modified | reviewed |
| `internal/app/auth.go` | modified | reviewed |
| `internal/app/connections.go` | modified | reviewed |
| `internal/app/connections_darwin.go` | modified | reviewed |
| `internal/app/connections_linux.go` | modified | reviewed |
| `internal/app/http.go` | added | reviewed |
| `internal/app/http_errors.go` | added | reviewed |
| `internal/app/idempotency.go` | added | reviewed |
| `internal/app/location.go` | modified | reviewed |
| `internal/app/notifications.go` | modified | reviewed |
| `internal/app/progress.go` | modified | reviewed |
| `internal/config/config.go` | modified | reviewed |
| `internal/config/notifications.go` | modified | reviewed |
| `internal/config/redaction.go` | added | reviewed |
| `internal/database/analytics.go` | renamed | reviewed |
| `internal/database/database.go` | modified | reviewed |
| `internal/database/notifications.go` | added | reviewed |
| `internal/database/rows.go` | added | reviewed |
| `internal/mirror/clone_darwin.go` | renamed | reviewed |
| `internal/mirror/clone_linux.go` | renamed | reviewed |
| `internal/mirror/descriptors.go` | renamed | reviewed |
| `internal/mirror/fingerprint.go` | renamed | reviewed |
| `internal/mirror/http.go` | renamed | reviewed |
| `internal/mirror/keep.go` | renamed | reviewed |
| `internal/mirror/listing-icons.html` | renamed | reviewed |
| `internal/mirror/listing.css` | renamed | reviewed |
| `internal/mirror/listing.go` | renamed | reviewed |
| `internal/mirror/listing.html` | renamed | reviewed |
| `internal/mirror/listing.js` | renamed | reviewed |
| `internal/mirror/mirror.go` | renamed | reviewed |
| `internal/mirror/roots.go` | renamed | reviewed |
| `internal/mirror/space_darwin.go` | renamed | reviewed |
| `internal/mirror/space_linux.go` | renamed | reviewed |
| `internal/mirror/static.go` | renamed | reviewed |
| `internal/mirror/storage.go` | renamed | reviewed |
| `internal/mirror/summary.go` | renamed | reviewed |
| `internal/mirror/validate.go` | renamed | reviewed |
| `internal/notification/credentials.go` | renamed | reviewed |
| `internal/notification/doc.go` | added | reviewed |
| `internal/notification/errors.go` | added | reviewed |
| `internal/notification/message.go` | renamed | reviewed |
| `internal/notification/redaction.go` | added | reviewed |
| `internal/notification/sender.go` | renamed | reviewed |
| `internal/notification/service.go` | renamed | reviewed |
| `internal/observability/doc.go` | added | reviewed |
| `internal/observability/log.go` | modified | reviewed |
| `internal/run/errors.go` | added | reviewed |
| `internal/run/redaction.go` | added | reviewed |
| `internal/run/run.go` | renamed | reviewed |
| `internal/run/snapshot.go` | renamed | reviewed |
| `internal/syncer/connect.go` | renamed | reviewed |
| `internal/syncer/doc.go` | added | reviewed |
| `internal/syncer/github.go` | renamed | reviewed |
| `internal/syncer/process.go` | renamed | reviewed |
| `internal/syncer/process_darwin.go` | renamed | reviewed |
| `internal/syncer/process_linux.go` | renamed | reviewed |
| `internal/syncer/redaction.go` | added | reviewed |
| `internal/syncer/runner.go` | renamed | reviewed |
| `internal/syncer/strays.go` | renamed | reviewed |
| `internal/syncer/strays_darwin.go` | added | reviewed |
| `internal/syncer/strays_linux.go` | added | reviewed |
| `internal/timefmt/timefmt.go` | added | reviewed |
| `web/.prettierignore` | added | reviewed |
| `web/e2e/support.ts` | added | reviewed |
| `web/embed.go` | modified | reviewed |
| `web/eslint.config.js` | added | reviewed |
| `web/package.json` | modified | reviewed |
| `web/playwright.config.ts` | modified | reviewed |
| `web/src/app/App.vue` | modified | reviewed |
| `web/src/app/api.ts` | modified | reviewed |
| `web/src/app/config-errors.ts` | added | reviewed |
| `web/src/app/error-text.ts` | added | reviewed |
| `web/src/app/errors.ts` | added | reviewed |
| `web/src/app/labels.ts` | added | reviewed |
| `web/src/app/router.ts` | modified | reviewed |
| `web/src/app/server-time.ts` | added | reviewed |
| `web/src/app/state.ts` | modified | reviewed |
| `web/src/app/types.ts` | modified | reviewed |
| `web/src/app/use-query-state.ts` | added | reviewed |
| `web/src/components/ConfirmDialog.vue` | modified | reviewed |
| `web/src/components/FieldIssue.vue` | added | reviewed |
| `web/src/components/ModalDialog.vue` | added | reviewed |
| `web/src/components/OptionCard.vue` | modified | reviewed |
| `web/src/components/PaginationBar.vue` | renamed | reviewed |
| `web/src/components/RefreshButton.vue` | modified | reviewed |
| `web/src/components/SelectField.vue` | modified | reviewed |
| `web/src/components/StackedTime.vue` | modified | reviewed |
| `web/src/components/StatusBadge.vue` | modified | reviewed |
| `web/src/components/ToastStack.vue` | modified | reviewed |
| `web/src/features/analytics/AnalyticsPage.vue` | renamed | reviewed |
| `web/src/features/analytics/TrendChart.vue` | modified | reviewed |
| `web/src/features/overview/OverviewPage.vue` | renamed | reviewed |
| `web/src/features/runs/BatchesPage.vue` | added | reviewed |
| `web/src/features/runs/RecordsPage.vue` | renamed | reviewed |
| `web/src/features/runs/RunDetail.vue` | modified | reviewed |
| `web/src/features/runs/index.ts` | added | reviewed |
| `web/src/features/runs/useRunDetail.ts` | modified | reviewed |
| `web/src/features/runs/useRunPage.ts` | modified | reviewed |
| `web/src/features/schedules/SchedulesPage.vue` | renamed | reviewed |
| `web/src/features/settings/EnvironmentChecks.vue` | modified | reviewed |
| `web/src/features/settings/NotificationSettings.vue` | renamed | reviewed |
| `web/src/features/settings/SettingsPage.vue` | renamed | reviewed |
| `web/src/features/sources/GitHubBatch.vue` | modified | reviewed |
| `web/src/features/sources/SourceEditor.vue` | modified | reviewed |
| `web/src/features/sources/SourcesPage.vue` | renamed | reviewed |
| `web/src/features/sources/githubBatch.ts` | modified | reviewed |
| `web/src/layouts/AppShell.vue` | renamed | reviewed |
| `web/src/lib/format/bytes.ts` | added | reviewed |
| `web/src/lib/format/date.ts` | added | reviewed |
| `web/src/lib/format/duration.ts` | added | reviewed |
| `web/src/lib/format/number.ts` | added | reviewed |
| `web/src/lib/motion/tabIndicator.ts` | modified | reviewed |
| `web/src/lib/pagination/usePagination.ts` | modified | reviewed |
| `web/src/styles/global.css` | modified | reviewed |
| `web/src/styles/tokens.css` | added | reviewed |
| `web/src/test/setup.ts` | added | reviewed |
| `web/stylelint.config.js` | added | reviewed |
| `web/tsconfig.json` | modified | reviewed |
| `web/vite.config.ts` | modified | reviewed |
| `web/vitest.config.ts` | added | reviewed |

</details>

<details>
<summary>排除清单与人工补审范围（均已核对）</summary>

| 文件 | 状态 | OCR 排除原因 |
| --- | --- | --- |
| `AGENTS.md` | modified | unsupported_ext |
| `Go代码规范.md` | added | unsupported_ext |
| `README.en.md` | modified | unsupported_ext |
| `README.md` | modified | unsupported_ext |
| `Vue代码规范.md` | added | unsupported_ext |
| `Web代码规范.md` | added | unsupported_ext |
| `cmd/mirrordock/cli_test.go` | modified | default_path |
| `cmd/mirrordock/main_test.go` | modified | default_path |
| `cmd/mirrordock/version_build_test.go` | modified | default_path |
| `cmd/mirrordock/version_test.go` | modified | default_path |
| `docs/ACCEPTANCE.md` | modified | unsupported_ext |
| `docs/API.md` | added | unsupported_ext |
| `docs/DEPLOYMENT.md` | modified | unsupported_ext |
| `docs/DESIGN.md` | modified | unsupported_ext |
| `docs/IMPLEMENTATION.md` | modified | unsupported_ext |
| `go.mod` | modified | unsupported_ext |
| `go.sum` | modified | unsupported_ext |
| `internal/app/analytics_test.go` | modified | default_path |
| `internal/app/app_test.go` | modified | default_path |
| `internal/app/apply_test.go` | modified | default_path |
| `internal/app/auth_test.go` | modified | default_path |
| `internal/app/decode_fuzz_test.go` | added | default_path |
| `internal/app/empty_body_test.go` | added | default_path |
| `internal/app/failure_test.go` | added | default_path |
| `internal/app/helpers_test.go` | modified | default_path |
| `internal/app/location_test.go` | modified | default_path |
| `internal/app/notifications_test.go` | modified | default_path |
| `internal/app/preflight_test.go` | modified | default_path |
| `internal/app/progress_test.go` | modified | default_path |
| `internal/app/testdata/fuzz/FuzzDecodeRequest/unknown` | added | default_path |
| `internal/config/config_test.go` | modified | default_path |
| `internal/config/helpers_test.go` | deleted | default_path |
| `internal/config/source_url_test.go` | modified | default_path |
| `internal/config/testdata/fuzz/FuzzProxyURL/loopback` | added | default_path |
| `internal/config/validation_fuzz_test.go` | added | default_path |
| `internal/database/database_test.go` | modified | default_path |
| `internal/database/idempotency_test.go` | added | default_path |
| `internal/mirror/database_test.go` | added | default_path |
| `internal/mirror/descriptors_test.go` | renamed | default_path |
| `internal/mirror/http_test.go` | renamed | default_path |
| `internal/mirror/issues_test.go` | renamed | default_path |
| `internal/mirror/keep_test.go` | renamed | default_path |
| `internal/mirror/listing_test.go` | renamed | default_path |
| `internal/mirror/review_test.go` | renamed | default_path |
| `internal/mirror/roots_test.go` | renamed | default_path |
| `internal/mirror/static_test.go` | renamed | default_path |
| `internal/mirror/storage_test.go` | renamed | default_path |
| `internal/mirror/validate_test.go` | renamed | default_path |
| `internal/notification/database_test.go` | added | default_path |
| `internal/notification/helpers_test.go` | added | default_path |
| `internal/notification/message_test.go` | renamed | default_path |
| `internal/notification/notifications_test.go` | renamed | default_path |
| `internal/notifications/helpers_test.go` | deleted | default_path |
| `internal/observability/log_test.go` | modified | default_path |
| `internal/run/actions_test.go` | added | default_path |
| `internal/run/analytics_test.go` | added | default_path |
| `internal/run/concurrency_test.go` | renamed | default_path |
| `internal/run/conflict_test.go` | renamed | default_path |
| `internal/run/database_test.go` | added | default_path |
| `internal/run/helpers_test.go` | added | default_path |
| `internal/run/issues_test.go` | added | default_path |
| `internal/run/logs_test.go` | added | default_path |
| `internal/run/proxy_test.go` | renamed | default_path |
| `internal/run/runs_test.go` | renamed | default_path |
| `internal/run/snapshot_test.go` | renamed | default_path |
| `internal/run/storage_test.go` | renamed | default_path |
| `internal/runs/actions_test.go` | deleted | default_path |
| `internal/runs/analytics_test.go` | deleted | default_path |
| `internal/runs/helpers_test.go` | deleted | default_path |
| `internal/runs/issues_test.go` | deleted | default_path |
| `internal/runs/logs_test.go` | deleted | default_path |
| `internal/sync/strays_darwin.go` | deleted | deleted |
| `internal/sync/strays_linux.go` | deleted | deleted |
| `internal/syncer/connect_test.go` | renamed | default_path |
| `internal/syncer/github_test.go` | renamed | default_path |
| `internal/syncer/progress_fuzz_test.go` | added | default_path |
| `internal/syncer/proxy_test.go` | renamed | default_path |
| `internal/syncer/runner_test.go` | renamed | default_path |
| `internal/syncer/strays_test.go` | renamed | default_path |
| `internal/syncer/testdata/fuzz/FuzzParseProgress/rsync` | added | default_path |
| `internal/timefmt/testdata/fuzz/FuzzTimeRoundTrip/utc` | added | default_path |
| `internal/timefmt/timefmt_test.go` | added | default_path |
| `staticcheck.conf` | added | unsupported_ext |
| `web/e2e/accessibility.spec.ts` | added | default_path |
| `web/e2e/analytics.spec.ts` | modified | default_path |
| `web/e2e/config-errors.spec.ts` | added | default_path |
| `web/e2e/github-batch.spec.ts` | modified | default_path |
| `web/e2e/log-controls.spec.ts` | modified | default_path |
| `web/e2e/mirrordock.spec.ts` | modified | default_path |
| `web/e2e/notifications.spec.ts` | modified | default_path |
| `web/e2e/overview-filter.spec.ts` | modified | default_path |
| `web/e2e/pagination.spec.ts` | modified | default_path |
| `web/e2e/preflight.spec.ts` | modified | default_path |
| `web/e2e/public-directory.spec.ts` | modified | default_path |
| `web/e2e/shell-scroll.spec.ts` | modified | default_path |
| `web/e2e/toasts.spec.ts` | modified | default_path |
| `web/package-lock.json` | modified | default_path |
| `web/src/app/api.test.ts` | added | default_path |
| `web/src/app/errors.test.ts` | added | default_path |
| `web/src/app/format.ts` | deleted | deleted |
| `web/src/app/use-query-state.test.ts` | added | default_path |
| `web/src/components/ConfirmDialog.test.ts` | added | default_path |
| `web/src/components/Modal.vue` | deleted | deleted |
| `web/src/components/ModalDialog.test.ts` | added | default_path |
| `web/src/components/PaginationBar.test.ts` | added | default_path |
| `web/src/features/runs/Batches.vue` | deleted | deleted |
| `web/src/features/runs/run-detail.css` | deleted | deleted |
| `web/src/features/runs/useRunDetail.test.ts` | added | default_path |
| `web/src/features/runs/useRunPage.test.ts` | added | default_path |
| `web/src/lib/format/format.test.ts` | added | default_path |
| `web/src/lib/pagination/usePagination.test.ts` | added | default_path |
| `代码规范.md` | added | unsupported_ext |
| `目录规范.md` | modified | unsupported_ext |

</details>

### 未验证项与产品边界

Linux 实机运行、HTTP/1.1 sendfile 系统调用观测、真实发行版全量同步、生产规模/硬件/SELinux/断电恢复、容器与 systemd 实际运行、真实通知渠道、Firefox/Safari/Edge 实际浏览器、远端 CI/发布均未在本轮执行。相关 Linux 平台代码通过交叉 vet/构建，但不能替代实机验收。所有测试仅使用临时目录、隔离端口和可控上游，没有启动示例完整发行版同步，没有修改用户镜像或凭据。

按 Go 规范首次接入工具的提交顺序，本轮先提交全量实现/测试/文档对齐，再单独提交 CI 门禁。仅本地提交，不推送，不创建发布。

---

## CLI 入口与配置加载对齐 Go代码规范 §7/§10（2026-10-23）

范围：`cmd/mirrordock` 按 Go代码规范 §10 与附录 A 对齐（新增 `log.go`；`configPath` 返回 `(path, explicit, err)`；冲突参数文本 `different files given for -c/--config: %q and %q`；`context.AfterFunc(ctx, stop)` 第二次信号强制退出；日志就绪后的失败只写日志；生命周期事件 `service_starting` / `service_listening` / `service_stopping` / `service_stopped`；关闭失败退出 1）。`internal/config` 入口改为 `config.Open(config.LoadSource)`，不再自行解析工作目录；配置与校验错误改为英文并带字段路径（`FieldError`）；`internal/observability` 日志初始化错误改为英文并接收调用方的 stdout。YAML 键、环境变量、默认路径、选择优先级、相对路径基准、默认值与 HTTP 行为未改。长期偏离记录在 README“代码例外”。

| 检查 | 结果 |
| --- | --- |
| `use-modern-go list --file-path cmd/mirrordock/main.go` | 已完整读取 Go 1.27.3 适用指南（`errors_as_type`、`context_after_func`、`slices`/`maps` 迭代器等）并在改动代码中采用 |
| `test -z "$(gofmt -l $(git ls-files -co --exclude-standard '*.go'))"` | 通过 |
| `go vet ./...` / `go vet -tags integration,scale ./...` | 通过 |
| `go test -count=1 ./...` | 通过 |
| `CGO_ENABLED=1 go test -race -count=1 ./cmd/... ./internal/config/...` | 通过 |
| `go test -tags integration -count=1 ./cmd/...` | 通过（含附录 C 的 `TestBuildVersion`） |
| 仓库外构建的二进制在空临时目录运行 | `--help` 退出 0 且只写 stdout；`--version` 退出 0；`-x`、`-c a -c b` 退出 2 并输出两行 stderr；`CONFIG_FILE=' '` 退出 1（`mirrordock: config: CONFIG_FILE is set but empty`）；`-c /nonexistent.yaml` 与缺失默认文件退出 1 且为英文；`CONFIG_FILE=/nonexistent.yaml mirrordock --help` 退出 0；运行后临时目录无残留文件 |
| 启动/关闭冒烟（临时目录、127.0.0.1 隔离端口、无同步源） | 记录 starting/listening/stopping/stopped，SIGTERM 后退出 0，stderr 为空；临时目录已删除 |
| OCR 托管审查 | `ocr delegate preview/rule --format json`（v1.13.3），当前代理审查，未调用外部 OCR LLM。可审文件共 7 个，已审 7 个、跳过 0 个，覆盖率 100%：`cmd/mirrordock/cli.go`、`cmd/mirrordock/main.go`、`cmd/mirrordock/log.go`（added）、`internal/app/app.go`、`internal/config/config.go`、`internal/config/notifications.go`、`internal/observability/log.go`。未发现需修复的正确性、安全或性能问题；已确认错误文本不含代理、Token 等字段值，未知通知渠道名以 `%q` 转义 |
| 排除与人工补审 | 8 个测试文件因 default_path 排除，README.md、README.en.md、docs/DEPLOYMENT.md、docs/IMPLEMENTATION.md、本记录因 unsupported_ext 排除，均已人工核对；AGENTS.md 与规范文件为既有未提交改动，本次未修改 |
| 未执行 | staticcheck、goimports、govulncheck、Web 构建与 E2E（未改 Web）、Linux/容器验收与性能测试 |

## 配置短参数改为 -c（2026-10-22）

范围：按用户要求将配置短参数 `-f` 替换为 `-c`，旧参数不再接受；`--config`、默认 `config.yaml`、相对路径基准与冲突检查保持原行为。同步 CLI 帮助、参数测试、中英文 README、AGENTS、DESIGN、DEPLOYMENT 与示例配置注释。

| 检查 | 结果 |
| --- | --- |
| `use-modern-go list --file-path cmd/mirrordock/cli.go` | 已完整读取 Go 1.27.3 适用指南；本次仅修改参数注册、校验与测试 |
| `npm --prefix web run build` | 通过，先生成 Go 嵌入所需的 Web 资源 |
| `go test -race -count=1 ./cmd/mirrordock` | 通过；覆盖 `-c file`、`-c=file`、`--config`、同值组合、空值/缺值、冲突与拒绝旧 `-f`，以及默认配置、帮助和版本 |
| `go vet ./cmd/mirrordock` / `git diff --check` | 通过 |
| OCR 托管审查 | `ocr delegate preview/rule --format json`（v1.13.2），当前代理审查，未调用外部 OCR LLM。可审文件共 2 个，已审 2 个、跳过 0 个，覆盖率 100%：`cmd/mirrordock/cli.go`、`configs/config.example.yaml`（均 modified）。未发现正确性、安全性或性能问题 |
| 排除与人工补审 | `cmd/mirrordock/cli_test.go` 因 default_path 排除；`AGENTS.md`、`README.md`、`README.en.md`、`docs/DESIGN.md`、`docs/DEPLOYMENT.md`、本验收记录因 unsupported_ext 排除。上述文件均人工核对；全仓检索确认有效使用说明已无旧参数 |
| 未执行 | 全量 Go 测试、Web E2E、Linux/容器验收与性能测试（本次仅 CLI 参数及说明变更）；未启动同步或读取用户运行配置 |

## 代码质量整改与工具链升级 Go 1.27.3 / Node 26.12.1（2026-10-22）

范围：全仓 Go 与 Web 源码的死代码、未用变量与命名可读性整改；Go 工具链 1.27.2 → 1.27.3；CI、Release 与 Dockerfile 的 Node 由浮动 24 改为 26.12.1（2026-10-21 安全补丁版）；依赖安全升级。配置协议、HTTP API 字段与页面行为未改动。用户要求：代码质量检查并全部修复、升级 Go 与 CI Node 以规避现有漏洞、提交推送并打新 tag。

- 静态检查：`staticcheck`（全部检查）、`deadcode`（含/不含测试）、`go fix -diff`、`vue-tsc --noUnusedLocals --noUnusedParameters`、Web 导出与 `global.css` 类名引用扫描、`govulncheck`、`npm audit`。
- 死代码：删除无任何调用方的 `runs.Engine.ValidateChange`、`runs.Engine.List`；仅测试使用的 `app.Open`、`config.Manager.Reload`、`runs.Engine.Get`/`ClearLogs`、`notifications.Service.Records` 移入各包 `helpers_test.go`，生产二进制不再包含（`app` 测试经 `openFile`/`reloadConfig` 辅助函数，跨包处改用 `GetContext`/`ReloadChecked`）；`runs_test.go` 一处从未读取的赋值；Web 删除未使用的 `.text-left`，`normalizePrefix` 取消无外部使用的导出。
- 可读性：HTTP 状态码数字改为 `http.Status*` 常量；`mirrors.Manager` 中含义易混的信号量重命名（`gates`→`largeDownloads`、`metadataGates`→`smallDownloads`、`listingGates`→`listingRenders`、`fileGates`→`openFiles`、`active`→`activeDownloads`、`bytes`→`sentBytes`），字段分组并注明锁的保护范围，`Stats` 的 JSON 键保持不变；`runs.Engine` 字段分组注释（锁顺序 configMu/logMu 先于 mu），`data`→`dataDir`，三处重复拼接的日志路径收敛为 `logDir`/`logPath`；`App` 的 `once`/`progress`/`listing`/`sse` 改为 `closeOnce`/`progressFrames`/`listingFrames`/`eventStreams`；`notifications.Service` 的 `testing`/`start` 改为 `testsInFlight`/`startOnce`；`config.Manager` 补字段说明；公开目录面包屑按 `go fix` 改用 `strings.Builder`。
- 保留未改：staticcheck ST1005 的 8 处（错误信息以 Release、Packages、Bot Token、Bark、Telegram 等专有名词开头，属面向用户的中文提示）。
- 版本与依赖：`go.mod` 为 `go 1.27.3`；Dockerfile 基础镜像 `golang:1.27.3-bookworm`、`node:26.12.1-bookworm-slim`（摘要经 Docker Hub registry API 解析的多架构索引）；CI 与 Release 的 `setup-node` 固定 `26.12.1`。Go 依赖升级 x/crypto 0.58.0、x/sys 0.49.0、klauspost/compress 1.20.3、modernc.org/sqlite 1.61.0（libc 1.79.0）等；Web 在 semver 范围内 `npm update`，修复 `npm audit` 报告的 vite 7.3.6 高危漏洞（升至 7.3.7），audit 0 漏洞。actions/checkout v7.0.1、setup-node v7.1.0、setup-go v7.0.0、goreleaser-action v7.3.0 与 GoReleaser v2.18.3 均已是最新稳定版且提交 SHA 一致，未改动。
- `govulncheck`：代码未调用任何已知漏洞；模块级仅 GO-2026-5932（`golang.org/x/crypto/openpgp` 已停止维护，上游无修复版本），本项目未导入该包。

| 检查 | 结果 |
| --- | --- |
| `staticcheck ./...` / `deadcode ./...` / `go fix -diff ./...` | 除上述 ST1005 外无输出 |
| `go mod verify` / `gofmt -l cmd internal` / `go vet ./...` / `GOOS=darwin GOARCH=arm64 go vet ./...` / `GOOS=linux GOARCH=amd64 go vet ./...` | 通过 |
| `go test -race -count=1 ./...`（`MIRRORDOCK_TEST_RSYNC=.cache/rsync/rsync-3.5.1/rsync`，本机 macOS/APFS，Go 1.27.3） | 9 个包通过；320 个测试（含子测试）通过，0 失败，0 跳过，无 data race |
| `go test ./internal/mirror -run '^$' -bench BenchmarkPublicHTTP -benchmem`（本机合成，Apple M2 Max） | 6483 MB/s，10362 B/op，136 allocs/op；只用于确认字段改名后下载路径无回退，不代表 Linux 性能 |
| `GOOS=linux GOARCH={amd64,arm64} CGO_ENABLED=0 go build` | 通过（仅编译） |
| `npm --prefix web run format:check` / `npm --prefix web run build`（含 `vue-tsc`） / `npm audit` | 通过；0 漏洞 |
| E2E 全量（scratchpad 临时二进制，rsync 3.5.1，本机 Node 26） | 19 通过、0 跳过 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule` 选出 14/14 可审文件（ci.yml、release.yml、Dockerfile、config.example.yaml、app.go、config.go、mirrors/{http,listing,mirrors,static}.go、notifications/service.go、runs/runs.go、githubBatch.ts、global.css），由当前会话审查，覆盖率 100%；排除的 AGENTS/README/README.en/IMPLEMENTATION（unsupported_ext）、go.mod/go.sum/package-lock.json 与 19 个测试文件（default_path）已人工核对。修复 2 项：本轮新增的 `runs.Engine` 注释漏写 `mu` 同时保护 `clearing`、`logMu` 也先于 `mu` 获取；测试钩子注释未覆盖两个间隔字段 |
| 未执行 | Docker 镜像构建（本机 docker 无 buildx，仅核对基础镜像摘要）；`goreleaser check` 与快照构建（本机未安装，由 CI 执行）；CI 上 Node 26.12.1 与 Go 1.27.3 的实际运行待推送后确认；Linux/EL 实机 |

## 放宽来源切换预算测试的时间余量（2026-10-19）

范围：仅 `internal/run/runs_test.go` 的 `TestFailoverSharesTimeoutAndLocalErrorsDoNotSwitch`。CI 运行 39231050434（提交 6dfee60，只改文档与许可证）中 `/true` 子测试失败：预算 120ms、首个来源 65ms 失败后，切换来源的两次 SQLite 持久化（fsync，测试库位于 CI 的 loop XFS 上）耗尽剩余约 55ms，第二个来源尚未启动即超时（`calls=1`，`ElapsedMillis=0.135`）。预算计入来源切换时间是既定行为，判定为测试余量不足，产品代码未改。

- 调整：预算改为 400ms，首个来源 200ms 时失败，给切换留 200ms；断言改为 `ElapsedMillis ≤ 0.5`。共享预算约 0.4s 超时，若切换时重置预算则至少 0.6s，仍能区分。

| 检查 | 结果 |
| --- | --- |
| 目标测试 | `go test -race -count=20 -run TestFailoverSharesTimeoutAndLocalErrorsDoNotSwitch ./internal/run` 通过（本机 macOS/APFS） |
| 所在包 | `go test -race -count=1 ./internal/run` 通过；`gofmt -l` 无输出 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview` 以 default_path 排除测试文件（0 个可审文件），按 Go 规则组人工审查该 diff，无问题 |
| 未执行 | 未在 CI 慢盘上复现或验证，待推送后由 CI 确认 |

## README 改为开源项目结构并新增英文版（2026-10-19）

范围：重写 `README.md`（中文主入口：特性、运行要求、快速开始、配置要点、部署、开发、版本发布、文档索引、目录树与目录例外、贡献、许可证说明），新增英文翻译 `README.en.md`；原 README 的详细运维内容原文迁入新增的 `docs/DEPLOYMENT.md`，构建、版本、发布、开发验证、CI 与依赖校验原文迁入 `docs/IMPLEMENTATION.md`；同步 `docs/DESIGN.md`、`docs/IMPLEMENTATION.md` 的入口引用；`.goreleaser.yaml` 发布包增加 `README.en.md` 与 `docs/DEPLOYMENT.md`，并更新注释引用。随后按用户决定新增根 `LICENSE`（Apache License 2.0 官方全文，SHA-256 与 apache.org 及 Go 模块缓存中的标准副本一致），README 加许可证徽章与说明，发布包加入 `LICENSE`。之后按用户决定删除 `scripts/build.sh`（仅包装 Web 构建与 `go build` 两条命令，CI、Dockerfile 与 GoReleaser 均未使用），README、README.en.md 与 IMPLEMENTATION 改为直接写两条命令。随后按用户决定移除整个 `scripts/`：`ci-test-env.sh` 原样改为组合 Action `.github/actions/test-env/action.yml`（`shell: sh`，保持原 POSIX 语义），`ci.yml` 两处改为 `uses: ./.github/actions/test-env`；删除 `generate-favicon.py`（只支持 rect/polygon，已无法处理当前含 path 的 logo）与 `import-directory-icons.py`，`listing-icons.html` 文件头改为记录上游来源与手工维护约定；README 目录树与 IMPLEMENTATION 同步。业务代码与配置协议未改动。用户要求：按一般开源项目重写 README，中英文一起提供，提交并推送。

| 检查 | 结果 |
| --- | --- |
| 文档链接 | 脚本检查 README.md、README.en.md、DEPLOYMENT、IMPLEMENTATION、DESIGN 的全部相对链接与锚点，0 处失效 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule`（v1.13.1）选出 1/1 可审文件 `.goreleaser.yaml`（YAML 规则组），由当前会话审查，覆盖率 100%，无问题；5 个 Markdown 文件因 unsupported_ext 排除，已人工核对命令、路径与原 README 内容一致 |
| 移除 scripts/ 后续检查 | `ocr delegate preview/rule`（托管模式，未调用外部 LLM）选出 3/3 可审文件：`ci.yml`、`listing-icons.html`、新增 `action.yml`，由当前会话审查，覆盖率 100%，无问题（`action.yml` 被 OCR 套用 Issue 模板规则，按组合 Action 语义人工补审）；4 个删除的脚本与 4 个 Markdown 文件排除。Action 主体与原脚本逐行对比，仅去掉 Actions 内恒成立的 `GITHUB_ENV` 检查；Ruby YAML 解析 `action.yml`、`ci.yml` 通过；`go test -count=1 ./internal/mirror` 通过 |
| 未执行 | 按用户要求本次不运行测试；本机无 goreleaser，未执行 `goreleaser check`，发布包新增文件由推送后 CI 的 GoReleaser 快照作业验证 |

## GitHub Actions CI 与 GoReleaser 多目标发布（2026-10-19）

范围：新增 `.github/workflows/ci.yml`（初版含 macOS 运行器，按用户决定改为仅 Linux，macOS 只交叉检查）、`.github/workflows/release.yml`、`.goreleaser.yaml`、`scripts/ci-test-env.sh`；修改 README（发布、CI、目录树、目录例外 `bin/release/`）。业务代码、配置协议与 Dockerfile 未改动。用户要求：加 GitHub CI，并用 GoReleaser 在提交 tag 后产出多架构、多目标的 Release 文件。

- 目标：linux amd64/arm64/ppc64le/s390x/riscv64/loong64，darwin amd64/arm64。`linux/386`、`linux/arm` 因 `internal/mirror/clone_linux.go` 的 `Stat_t.Ctim` 为 int32 无法编译，不发布（未改源码）。
- 发布构建不注入 `main.version`，依赖 Go 从 Git tag 写入的模块版本；GoReleaser 输出放在忽略的 `bin/release/`，避免根 `dist/` 让 Go 标记 dirty。
- Action 全部按提交 SHA 固定（checkout v7.0.1、setup-go v7.0.0、setup-node v7.1.0、goreleaser-action v7.3.0，SHA 经 `git ls-remote` 取得）；发布作业不用依赖/构建缓存。

| 检查 | 结果 |
| --- | --- |
| `actionlint`（v1.7.12，未启用 shellcheck） | 两个工作流通过 |
| `goreleaser check`（v2.18.3） | 临时克隆加 remote 后通过；本仓库无 remote 时报 `no remote configured`，属预期 |
| 临时克隆打 `v0.9.0-rc.1` 后 `goreleaser release --clean --skip=publish,announce` | 8 个目标全部构建并打包，生成 `checksums.txt`；`file` 确认各架构；`go version -m` 显示 `mod mirrordock v0.9.0-rc.1`、`vcs.modified=false`；`--version` 输出与发布工作流核对字符串完全一致；tar 包含二进制、README、示例配置与 systemd 单元 |
| 再提交一次后 `goreleaser release --snapshot --clean` | 通过；二进制显示 Go 伪版本，未冒充 tag |
| `scripts/ci-test-env.sh` + Go 检查（colima 特权容器 ubuntu:24.04 arm64，非 root + sudo，Go 1.27.2） | 脚本安装 xfsprogs、挂载 16G 稀疏 XFS（reflink=1）为 TMPDIR，使用 rsync 3.2.7；`go mod verify`、gofmt、`go vet ./...` 通过；`go test -race -count=1 ./...` 9 个包全部通过（未统计子测试跳过数） |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule`（v1.13.1）选出 4/4 可审文件（工作流规则组、YAML 组、通用组），由当前会话审查，覆盖率 100%；排除的 README.md（unsupported_ext）已人工核对。修复 3 项：作业缺 `timeout-minutes`；发布作业恢复 npm/Go 缓存存在缓存投毒面（改为不用缓存）；`git rev-parse --short=12` 在前缀冲突时可能长于 12 位，与 Go 固定截断不一致（改为 `cut -c1-12`）。容器验证另发现 gofmt 步骤 `tee /dev/stderr` 在 sudo 切换身份时无权限，改为变量判断 |
| 未执行 | GitHub 上真实运行（仓库当前无 remote，未推送）；Linux 下 E2E 与 Playwright 依赖安装；macOS 上的 Go 测试（按用户决定 CI 不设 macOS 运行器，仅 `GOOS=darwin go vet` 与交叉编译）；x86_64 运行器（容器为 arm64）；ppc64le/s390x/riscv64/loong64 与 darwin amd64 二进制仅交叉编译，未实机运行；真实 GitHub Release 上传 |

## 运行记录处理结果上下排列、失败原因只显示主原因（2026-10-18）

范围：修改 `web/src/features/runs/Records.vue`、`web/src/styles/global.css`；测试 `web/e2e/pagination.spec.ts`。用户要求：成功记录“成功发布”与“复用”上下两行，避免各行长短不一；失败原因只写主原因，列同样收窄。

- 成功：第一行“成功发布 · N 项”，有复用时第二行“复用 N 项”。
- 失败：第一行错误码，第二行只取错误首个非空行，单行省略，悬停 `title` 显示完整错误；完整内容仍在详情。表头与单元格统一居中。
- 第 6 列固定 200px（原为吃满剩余宽度），表格最小宽度 990px，各列按比例分配多余宽度。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` / `npm --prefix web run build` | 通过 |
| `pagination.spec.ts`（scratchpad 临时二进制） | 通过；失败页签表头断言由“第 6 列居左”改为全部居中 |
| 截图 | scratchpad 临时用例 mock 运行列表，1440px 截成功页签（含 40 项 + 复用 40 项）与失败页签（长多行 source_exhausted、task_timeout）人工核对，临时文件不入库 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule`（v1.13.1）选出 2/2 可审文件（通用组），覆盖率 100%；排除的 `pagination.spec.ts`（default_path）已人工核对。无发现 |
| 未执行 | 其余 E2E 未全量重跑（仅运行记录表格展示变更） |

## 镜像根配置外内容无条件公开（2026-10-18）

范围：新增 `internal/mirror/static.go`、`static_test.go`；修改 `internal/mirror/http.go`（抽出 `serveFile`/`readListing`，删除只列路由的 `virtual`）、`mirrors.go`（保存已配置 dest 集合）、`internal/config/config.go`（导出 `ReservedTopLevel`）、README、DESIGN。用户确定：放进镜像根即公开，不设开关、不隐藏点文件、不为误放兜底；只保留保护程序正确性的排除（`.mirrordock`、同步临时名、保留顶级路由、已配置 dest、越出镜像根）。

- 未命中发布路由的请求由静态区从镜像锚点 os.Root 读取；程序逐段解析符号链接，任一步进入排除路径即 404，打开后用 SameFile 核对解析时的 lstat。已删除源的残留别名、指向配置源的链接无法经静态区进入发布代。
- 已配置 dest 的上级目录合并列出发布项目与磁盘文件；项目始终列出，文件分享剩余的 1000 项名额（审查发现的问题，已修复）。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` / `npm --prefix web run build` | 通过（本次无前端源码改动） |
| `go vet ./...` | 通过 |
| `go test -race -count=1 ./...`（`MIRRORDOCK_TEST_RSYNC=.cache/rsync/rsync-3.5.1/rsync`） | 9 个包通过；320 个测试（含子测试）通过，0 失败，0 跳过，无 data race |
| 新增 `static_test.go` | 首页合并项目/静态目录/文件/点文件，隐藏 `.mirrordock`、`admin`、`*.part`；下载、HEAD、Range、目录重定向；改名替换后内容与 ETag 更新、新文件无需重载即列出；相对链接可跟随且按目标类型列出，指向配置源、`.mirrordock`、绝对路径、越界和自环的链接 404 且不列出；未发布/校验失败的 dest（含嵌套 `el/9`）404 且不列出，上级目录其他文件可见；移除源后残留别名 404；FIFO 不列出不提供；超过 1000 项时项目仍列出并提示截断。变异检查：去掉 dest 精确匹配后用例失败 |
| E2E 全量（scratchpad 临时二进制，rsync 3.5.1） | 19 通过、0 跳过 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule` 选出 4/4 可审文件（Go 组），覆盖率 100%；排除的 README、DESIGN（unsupported_ext）与 `static_test.go`（default_path）已人工核对。发现 1 项中危并修复：上级目录静态条目超过 1000 时，排序截断可能挤掉发布项目 |
| 未执行 | Linux 实机与真实发行版验收；静态区 sendfile 命中未做系统调用观测（与发布文件共用 `serveFile`，未单独验证）；解析后到打开前的替换只做 SameFile 核对，未做并发竞态压测 |

## 公开目录“管理后台”链接与管理台侧栏样式统一（2026-10-18）

范围：修改 `internal/mirror/listing.css`、DESIGN。用户选定：前台“管理后台”沿用管理台侧栏“镜像目录”链接的样式（`--brand` 绿、600 字重、13.5px、悬停下划线），仍在当前页打开、不带 ↗ 图标；去掉原灰字与浅绿底悬停块。

| 检查 | 结果 |
| --- | --- |
| `go vet ./internal/mirror/`、`go test -count=1 ./internal/mirror/` | 通过 |
| 截图 | 临时渲染页面，Playwright 截 1280px 页头、悬停态与 375px 页头：绿色加粗，悬停出现下划线，窄屏不换行 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule` 选出 1/1 可审文件（`listing.css`，通用组），覆盖率 100%；排除的 DESIGN、ACCEPTANCE 已人工核对。无发现 |
| 未执行 | E2E 未重跑（仅样式变更，链接文字与 href 不变） |

## 公开目录目录行去掉尾部斜杠、悬停下划线可见（2026-10-18）

范围：修改 `internal/mirror/listing.html`、`listing.css`、DESIGN；测试 `listing_test.go`、`public-directory.spec.ts`。用户要求：目录名后不带“/”，改为常见的鼠标移入时名称下方出现横线。

- 目录行只渲染名称；删除 `.dir-slash`。`tr.is-dir` 保留，用于手机布局取消图标占位。
- 原悬停下划线颜色为 `--line-strong`，且 `.filename` 行盒仅 17px、`overflow:hidden` 把位移 3px 的下划线裁掉，实际看不到。改为随文字颜色、1px 粗，并给 `.filename` 设 22px 行高留出空间；文件行同样受益。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` / `npm --prefix web run build` | 通过 |
| `go vet ./internal/mirror/`、`go test -count=1 ./internal/mirror/ ./internal/app/` | 通过；断言目录行为 `title="folder"><span class="filename">folder</span>`、无文件夹图标 |
| `public-directory.spec.ts`（scratchpad 临时二进制） | GitHub 项目用例通过，375px 截图核对；依赖现代 rsync 的首例**跳过未执行** |
| 悬停 | 临时渲染混合目录，Playwright 悬停目录名：计算样式为 underline，截图可见横线（修复行高前同一截图看不到） |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule` 选出 2/2 可审文件（通用组），覆盖率 100%；排除的 DESIGN、`listing_test.go`、`public-directory.spec.ts` 已人工核对。无发现 |
| 未执行 | 依赖现代 rsync 的 E2E；其余 E2E 未全量重跑 |

## 公开目录页头管理入口与目录行去图标（2026-10-18）

范围：修改 `internal/mirror/listing.html`、`listing.css`、`listing.go`、DESIGN；测试 `internal/mirror/listing_test.go`、`web/e2e/public-directory.spec.ts`。用户要求：前台右上角“软件源镜像”改为进入后台的入口，未登录到登录页；目录行不要文件夹图标，文件图标保留。

- 页头：静态文字改为指向 `/admin/` 的“管理后台”链接（悬停浅绿底）。管理台未登录时本来就显示登录表单，后端与路由未改。
- 目录行（已被上一条替代：现不带“/”）：不再渲染 `file-icon folder`，改为名称后接浅色、`aria-hidden` 的“/”（与常见镜像站一致，链接可访问名仍为目录名）；`tr.is-dir` 在手机卡片布局中取消 34px 图标占位，使第二行与名称对齐。文件行的 Phosphor 图标与配色不变；空目录提示的文件夹插图保留。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` / `npm --prefix web run build` | 通过 |
| `go vet ./internal/mirror/`、`go test -count=1 ./internal/mirror/ ./internal/app/` | 通过；新增断言：页头链接、`is-dir` 行与“/”标记存在，且无 `file-icon folder` |
| `public-directory.spec.ts`（scratchpad 临时二进制） | GitHub 项目用例通过；1440/375 截图核对目录行、项目表与页头。首例（含新加的“管理后台”点击到登录页、目录行无图标断言）需 `MIRRORDOCK_TEST_RSYNC`，本机仅 openrsync，**跳过未执行** |
| 混合目录（3 目录 + 6 文件）渲染 | 用临时测试渲染模板后以 Playwright 截 1280/375 图人工核对，临时文件已删除 |
| open-code-review | 托管模式，未调用外部 LLM；`ocr delegate preview/rule` 选出 3/3 可审文件（Go 组 1、通用组 2），由当前会话审查，覆盖率 100%；排除的 `listing_test.go`、`public-directory.spec.ts`（default_path）与 DESIGN（unsupported_ext）已人工核对。无发现：模板仍经 `html/template` 转义，脚本未变故 CSP 哈希不变，不涉及下载热路径 |
| 未执行 | 依赖现代 rsync 的 E2E；其余 E2E 未全量重跑 |

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

范围：`internal/config/config.go`（`settings.rsync.proxy`、源 `rsync.proxy_mode`/`rsync.proxy`、`ParseProxyField`）、新增 `internal/sync/connect.go`（`rsync-connect` 内部命令）、`internal/sync/{runner,process}.go`、`internal/run/{runs,snapshot}.go`、`cmd/mirrordock/main.go`、`web/src/app/types.ts`、`web/src/features/settings/{Settings,EnvironmentChecks}.vue`、`web/src/features/sources/SourceEditor.vue`、`configs/config.example.yaml`、README/DESIGN；测试 `internal/config/config_test.go`、新增 `internal/run/proxy_test.go`、`internal/sync/connect_test.go`、`internal/sync/{runner,strays}_test.go`（`command` 新增 env 参数、复用 daemon 启动辅助）、`web/e2e/{mirrordock,pagination}.spec.ts`。GitHub Release 与 Reposync 行为未改动。

- 配置：全局 `settings.rsync.proxy` 写法与 `settings.github.proxy` 相同；源 `rsync.proxy_mode` 省略/`global`/`direct`/`custom`，仅 custom 可填且必须填 `rsync.proxy`。页面：系统设置 Rsync 页“03 · 网络”；源编辑器“代理 · proxy_mode”三选一，跟随全局保存为省略（YAML 不新增键），切换离开自定义时清除地址。环境检查“下载代理”改名“GitHub 代理”，新增“Rsync 代理”行（全局状态与按源覆盖计数）。
- 执行：每次尝试从当前 YAML 解析代理；快照保留模式、清除地址；日志只写代理协议与主机端口，代理 URL 与凭据加入脱敏列表。rsync 经 `RSYNC_CONNECT_PROG` 启动 `mirrordock rsync-connect`（单引号转义路径，`%` 转义为 `%%`），代理/目标/连接超时经环境变量传递；rsync 环境始终剔除继承的 `RSYNC_PROXY`、`RSYNC_CONNECT_PROG`。**行为变化：此前 rsync 继承启动环境的 `RSYNC_PROXY`，现在只使用 YAML 中的代理。**
- 失败分类：代理不可达（可重试、`ProxyFailure`，本轮不切换备用来源，重试耗尽提示“代理不可用”）；认证/协议/配置错误（不可重试）；代理拒绝目标（HTTP 403、SOCKS 规则拒绝：不可重试的来源失败）；其他目标失败（可重试的来源失败）。隧道建立后的转发错误不再输出分类诊断，避免覆盖 rsync 自身判断（例如 unknown module 仍为不可重试的 `upstream_error`）。

| 检查 | 结果 |
| --- | --- |
| `go vet ./...` / `go test -race -count=1 ./...`（`MIRRORDOCK_TEST_RSYNC=.cache/rsync/rsync-3.5.1/rsync`） | 通过 |
| 真实 rsync 3.5.1 经代理（`TestSystemRsyncThroughProxy`，本机合成） | 来源主机为只有代理能到达的 `mirror.invalid`：HTTP CONNECT 与 SOCKS5h（均带认证）完成传输；经代理访问不存在模块保持 `upstream_error`；代理不可达得到可重试 `rsync_proxy`，消息与日志不含凭据 |
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

范围：`internal/config/config.go`（新增 `settings.concurrency`）、`internal/run/run.go`（固定 2 个 worker 改为按类型调度）、`web/src/features/settings/Settings.vue`、`web/src/app/types.ts`、`configs/config.example.yaml`、README/DESIGN；测试 `internal/config/config_test.go`、新增 `internal/run/concurrency_test.go`、`web/e2e/mirrordock.spec.ts`。

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

## 接口规范对齐与兼容代码清理（2026-10-27）

范围：按代码规范 §5 与 Go代码规范 §4、§6、§7、§9 对齐管理 API，删除未发布阶段遗留的兼容代码。

- 接口：新增 `GET /healthz`、`GET /api/info`；`/api/auth/status` 改为 `/api/auth/session`，`POST /api/sources/{id}/run` 改为 `/runs` 并支持 `Idempotency-Key`（SQLite `idempotency_keys`，24 小时）；配置与通知以 `ETag` / `If-Match` 做乐观并发（412 `precondition_failed` 取代 409 `config_conflict`）；通知渠道改为 `PATCH` + JSON Merge Patch；无响应体的操作返回 204；查询与路径参数错误返回 400；非 JSON 请求体 415；405 带 `Allow` 的 JSON 错误。
- 中间件：请求 ID、带 `request_id` 的访问日志、panic 恢复、安全响应头；`http.Server` 增加 `ReadTimeout`、`WriteTimeout`，SSE 每帧续期、公开下载与日志下载解除写截止时间（由连接停滞监视兜底）。
- 字段：时间改用 `internal/timefmt.Time`（UTC 毫秒）；大小与速度字段带单位（`transferred_bytes`、`total_bytes`、`speed_bytes_per_second`、`*_bytes`、`sent_bytes`、`next_offset_bytes`）；任务错误码改为 snake_case；删除 `file_capacity`、中文旧记录与无 `code` 旧事件的回退。
- 日志：组件改为构造参数注入 `*slog.Logger`，删除 `slog.SetDefault`；事件名改为 snake_case。YAML 依赖改为 `go.yaml.in/yaml/v3`。测试中等待辅助进程启动的 `time.Sleep` 改为就绪握手，其余为轮询或注明的观察窗口。

验证（本机 macOS，合成测试）：Go 检查脚本（mod verify/tidy、gofmt、goimports、vet 含 linux 与 tags、staticcheck、`go test -race`、govulncheck）通过；Web format/lint/typecheck/vitest（18 项）/build 通过；Playwright E2E 21 项通过（首轮 `config-errors` 因旧构建产物失败，重建后通过）。

OCR 托管审查：`ocr delegate rule` 解析 `internal/app/{http,idempotency,notifications,app}.go`、`internal/database/database.go`、`internal/timefmt/timefmt.go`、`web/src/app/api.ts`、`NotificationSettings.vue` 的规则，由当前代理审查；修正两处：幂等结果保存改用脱离取消的上下文（客户端断开后重试不再重复建任务），`guardAPI` 的类型断言改为带检查。未调用外部 OCR LLM。

注意：旧格式（RFC 3339 纳秒、旧字段名）的本地 SQLite 运行记录不再可读，开发环境需清空数据目录。

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
