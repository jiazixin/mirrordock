# 验收记录

验收日期：2026-10-01。工具链为 Go **1.27.1**；本机为 macOS / arm64、Apple M2 Max。本文区分实际执行的检查与目标 Linux 服务器上尚未执行的验收。

各节记录对应当时版本；当前管理账号协议以末尾“配置账号与移除顶部栏追加验收”为准，前文初始化管理员的测试属于历史记录。

## 已执行

| 检查 | 结果与范围 |
| --- | --- |
| Vue 类型检查、Vite 生产构建 | 通过；真实 Vue 输出嵌入 Go 二进制，页面不使用模拟运行记录。 |
| Go 静态检查、竞态测试 | `go vet ./...`、`go test -race ./...` 通过；发布回滚、读者生命周期、超时/暂停/恢复、配置冲突、凭证隔离均有覆盖。 |
| 真实 rsync 协议 | 以官方 rsync 3.5.1 的本地构建启动隔离 daemon，实际传输、硬链接/符号链接、默认 delete 和 UID/GID 参数检查通过。未接触案例中的完整仓库。 |
| GitHub 下载器 | 本地 HTTP fixture 验证最新 Release 排序、仅下载 assets、续传、错误 Range 重启、附件替换和摘要校验；没有声称已同步线上 OpenList。 |
| HTTP 与发布隔离 | Range/ETag/HEAD、私有文件和越界链接、旧代回退、读者保护、SQLite 关闭后仍能下载、发布链接/数据库失败回滚、删除配置保留文件均通过。 |
| 资源准入 | 大文件下载池耗尽返回 503 / Retry-After，小文件与 HEAD 仍可正常响应，回归测试通过。 |
| TLS / HTTP2 / 无进展监测 | 本地真实自签名 TLS、HTTP/2 下载通过；真实 TCP 监测验证有进展连接继续服务、停滞连接关闭。 |
| 浏览器端到端 | Chromium 最后一次为 **1 passed，6.2s**。初始化管理员、页面新增源、src1 失败/src2 成功、发布、公开下载与 Range、详情、配置写回/保留注释、五页响应式均通过。视口宽度 1440、1024、736、375，无页面脚本错误或整页横向溢出。 |
| 干净源码构建与启动 | 不含 node_modules、dist、数据库、缓存或旧二进制的临时副本执行 npm ci、Vue 构建、Go 构建和测试通过。最小空源配置启动后 `/api/auth/status`、`/admin/`、`/admin/records`、`/` 均为 200，SIGTERM 正常退出 0。 |
| Linux 构建 | `CGO_ENABLED=0` 的 linux/amd64 与 linux/arm64 二进制构建通过；这是交叉编译结果，不能代替目标 Linux 运行测试。 |

测试端口与同步目录均隔离；临时程序和 daemon 在测试结束后停止。运行报告、截图与本地二进制被 Git 忽略。

## 性能实测及边界

`BenchmarkPublicHTTP` 使用真实本地 TCP HTTP 服务、8 MiB 测试文件和流式客户端，以 `testing.B.Loop` 重复完整下载。最后三次结果：

| 项目 | 结果 |
| --- | --- |
| 吞吐 | 5892.19、5941.20、5830.05 MB/s，即约 5.83–5.94 GB/s |
| 完整请求时间 | 1.424、1.412、1.439 ms/op |
| 分配 | 10774、9535、9542 B/op；146、122、122 allocs/op |

分配统计包含测试客户端与服务端。该结果为本机回环、页缓存条件下的合成基准，不是物理网卡、冷盘或生产集群吞吐承诺。原始输出见 `../test-results/download-benchmark-final.txt`。

代码保留 `*os.File`、标准库文件服务以及底层 `ReaderFrom` 委托，可走 Linux HTTP/1.1 sendfile 路径。TLS 使用 Go 标准库流式传输；没有将 HTTPS 描述为 sendfile。下载热路径不访问 SQLite/YAML，不逐块写日志、不哈希全文件、不动态压缩；这些结构检查与隔离测试已完成。

尚未在 Linux 上用 syscall tracing 证明实际 sendfile 调用，也未测物理网卡满载、冷缓存、RSS 峰值、长时间多客户端拥塞或真实 RPM/APT 客户端安装。部署后应在目标机器补充这些数据，不能用交叉编译和本机基准替代。

## Open Code Review 审查

已使用用户指定的 `open-code-review`，并应用 `use-modern-go` 的 Go 1.27.1 指南。外部 OCR LLM 审查命令在执行前被自动审批拒绝：它可能将整套新源码发送到尚未确认的外部 LLM 服务。该外部调用未执行，也没有绕过审批重试。

随后使用 [open-code-review-delegate](https://github.com/alibaba/open-code-review) 的本地工作流：OCR 的 `delegate preview` 枚举文件，`delegate rule` 解析规则，由当前 Codex 对每个文件实际审查；OCR 不发起外部 LLM 请求。50 个可审查文件全部完成，跳过 0 个，生产审查清单覆盖率 100%。默认排除的测试、锁文件和不支持扩展名的文件在报告中逐项列出原因；测试、依赖清单、部署文件、Logo 与文档另行检查。原有目录规范保持原文。

修复并验证的主要问题：

- 删除源配置后的旧代回收可能删除保留文件：回收保护全部持久发布指针，包括已从配置删除的源。
- 公开链接或数据库提交失败后可能出现提前发布：先验证文件根/链接，提交失败恢复旧链接和暂存，提交成功后才交换内存路由。
- 新旧 GitHub 附件大小相同、替换下载失败时可能错误复用旧文件：最终身份与续传身份分别保存，成功校验后再更新最终身份。
- 准备阶段中断导致不完整复制被当作完成：只在全部复制成功后写完成标记，继续时修复部分文件。
- 进度、日志与前端异步请求的边界问题：扫描/校验阶段不伪造传输百分比，日志写失败停止执行，过期响应不能覆盖新筛选结果，暂停/取消保留明确原因。
- 下载与回收资源生命周期：旧根读者计数保护、后台回收可取消、大小文件分池、流量统计保持原始文件传输链。

最终没有遗留本次审查中已确认的高/中严重度正确性问题。此结论是本地 delegate / Codex 审查结果，不是独立外部 LLM 审查结果。完整清单、规则和结构化报告位于 `../test-results/ocr-preview.json`、`ocr-rules.json`、`ocr-review.json`。

## 尚未执行及当前范围

Docker 守护进程不可用，因此没有执行容器构建/启动；Dockerfile 已检查，不能将其写为部署测试通过。EL8/EL9 上的 DNF reposync、NGINX 官方签名和实际发行版全量同步尚未执行。示例中占位 rsync 地址需要使用者替换。

当前记录按任务聚合，详情包含尝试、事件、日志和快照；没有每个 GitHub 资产的独立数据库检索页。reposync 的可靠总量未知时显示未知进度。详细实现边界见 [IMPLEMENTATION.md](IMPLEMENTATION.md)，启动命令与字段解释见 [README.md](../README.md) 和 [配置案例](../configs/config.example.yaml)。

## GitHub 代理追加验收 · 2026-10-01

新增 `settings.github.proxy` 和设置页输入/清空操作。支持 HTTP、HTTPS、SOCKS5、SOCKS5h，API 和附件共用；留空直连，忽略代理环境变量。代理 URL 可带认证，完整地址不返回读取 API、不进入运行快照，日志与错误脱敏。配置仍原子写回 YAML，其他页面的配置保存不会清空代理。

重新执行 Vue 类型检查/生产构建、`go vet ./...`、带本地 rsync fixture 的 `go test -race ./...`、本机及 Linux amd64/arm64 构建，均通过。新增实际本地协议测试：HTTP/HTTPS CONNECT、SOCKS5/SOCKS5h 用户密码认证与代理端域名解析、API 和附件传输、凭证不转发给附件服务、代理不可用禁止直连回退。TLS fixture 使用信任证书池，未关闭证书校验。

Chromium 增加代理写回、读取 API 隐藏原值、保存后清空输入、显式清空后直连状态的检查，结果 **1 passed（6.2s）**。没有接入用户的真实 sing-box 或线上 GitHub，测试全部使用隔离代理和本地 TLS 上游。前文干净副本启动和下载基准是首次实现基线，本次未重复这两项；下载处理链和依赖清单未修改。

OCR local delegate 按前次源码摘要识别并复审本次 8 个变化的生产文件，覆盖 8/8、跳过 0；其他 42 个文件摘要未变化，沿用前次审查。没有遗留确认的高/中严重度问题，也未重新尝试外部 LLM 调用。增量清单与规则见 `../test-results/ocr-proxy-preview.json`、`ocr-proxy-rules.json`、`ocr-proxy-review.json`，总审查清单摘要已更新。

## 字体、下拉菜单与图标追加验收 · 2026-10-01

按参考图风格改用系统无衬线字体与标准 400/500/600/700 字重，加深标题、正文、表格与辅助文字颜色。同步源筛选 1 处、源编辑 1 处、批次筛选 4 处、运行记录筛选 2 处、日志尝试 1 处，共 **9 处**全部改用 `SelectField`；生产 Vue 模板原生 `<select>` 为 0。菜单支持鼠标、方向键、Home/End、Enter、Escape、Tab、点击外部关闭，脱离面板裁切并根据视口定位。

新 Logo 为蓝底白色 M 的 SVG，侧栏和登录页共享矢量来源；同一 SVG 生成六尺寸 favicon（16/32/48/64/128/256）。`/favicon.ico` 和 `/admin/favicon.ico` 匿名访问均返回同一真实 ICO，HEAD 无正文且长度正确，未知图标返回 404；公开镜像目录也引用新图标。图标生成工具位于 `scripts/generate-favicon.py`，普通构建不需要 Python。

Vue 类型检查与 Vite 生产构建、`go vet ./...`、使用隔离 rsync daemon 的 `go test -race ./...` 均通过；本机与 Linux amd64/arm64 二进制重新构建。最终 Chromium 结果 **1 passed（8.0s）**：覆盖全部 9 个菜单实际选择、数值型日志尝试、弹窗内 Escape、键盘选择、外部点击/Tab、刷新期间菜单不关闭、字体计算值、ICO 内容一致性，以及 1440/1024/736/375 四种宽度的五页无整页横向溢出。已检查桌面、手机菜单和新 Logo 截图。首次追加测试的日志断言错误地期待非 verbose rsync 输出文件名，改为验证实际传输统计后通过；没有将失败测试记录为通过。

OCR 本地 delegate 复审本次 **14 个可审查文件，14/14，跳过 0**。为避免枚举用户新同步到项目目录的镜像文件，只将变化的源码与必要测试/资产复制到临时审查目录运行 preview/rule；没有复制用户配置、凭证或镜像文件。默认排除的 6 个文档/测试/图标另行检查，没有遗留确认的高/中严重度问题；外部 LLM 调用未执行。清单、规则和 SHA256 见 `../test-results/ocr-ui-preview.json`、`ocr-ui-rules.json`、`ocr-ui-review.json`。

所有同步测试仍使用临时目录与隔离端口，用户配置、镜像数据与正在运行的服务未修改或重启。新静态资源嵌入新二进制，需切换到新程序并重启才对现有服务生效；Linux 交叉编译不能替代目标平台运行验收。

## 数据库自动初始化追加验收 · 2026-10-01

删除独立的 `migrations/` SQL 文件及嵌入包，初始化语句直接内置在 `internal/database/database.go`，仍由 `database.Open` 在启动时执行。逐句核对与原 SQL 一致，现有表、索引、schema 版本和记录保持原样；无需手动执行 SQL。同步移除 Dockerfile 的旧 COPY，更新 README、设计、实现说明与实施计划；目录基线保持原文。

应用 `use-modern-go` 的 Go 1.27.1 完整指南。Vue 类型检查与 Vite 构建、`GOCACHE="$PWD/.cache/go-build" go vet ./...`、`GOCACHE="$PWD/.cache/go-build" MIRRORDOCK_TEST_RSYNC="$PWD/.cache/rsync/rsync-3.5.1/rsync" go test -race ./...` 均通过；数据库持久记录测试验证新库初始化、保存运行与关闭后重新读取，真实 rsync 使用隔离 daemon。

将当前已跟踪源码（含本次修改、排除已删除文件）复制到临时目录，不含用户配置、镜像、node_modules、dist 或旧二进制。在副本 `web/` 执行 `npm ci` 和 `npm run build`，再从副本根执行 `go build -trimpath -o mirrordock-init-check ./cmd/mirrordock`，通过。用新二进制及临时空源 YAML 启动两次：首次自动生成 `data.sqlite`、全部四张业务/schema 表与两项运行索引；文件权限 0600、WAL 正常；停机后插入的临时状态在第二次启动后仍保留。两次 `/api/auth/status`、`/admin/`、`/` 均返回 200，SIGTERM 退出码均为 0。原始启动结果见 `../test-results/init-smoke.json`。

初次使用 `--prefix` 安装临时副本依赖失败，报 `Missing: web@0.1.0 from lock file`；改在副本的真实 `web/` 工作目录安装后通过，未修改依赖清单或锁文件。首次 Go 检查因默认缓存写权限及本地测试监听被沙箱限制而失败；改用仓库缓存，并经自动审批允许本地测试端口后重跑通过。沙箱内依赖下载未完成，停止该临时目录的安装进程，经自动审批允许依赖下载后安装成功；这些初次失败不作为通过记录。

OCR 本地 delegate 审查覆盖 `internal/database/database.go`、`Dockerfile`，**2/2，跳过 0**，未发现确认的高/中严重度问题。默认排除的两项已删除 SQL/Go 文件（`deleted`）及五项 Markdown 文档（`unsupported_ext`，包含本节验收记录）另行检查；初始化内容完整保留，构建与文档不再依赖旧目录。审查仅使用隔离临时副本；外部 LLM 调用沿用此前被审批阻止后的本地工作流，本次未尝试。清单、规则与结构化结果见 `../test-results/ocr-init-preview.json`、`ocr-init-rules.json`、`ocr-init-review.json`。

Docker 守护进程仍不可用，本次容器构建/启动未执行；Linux 运行、浏览器 E2E 和下载性能基准未重复，本次修改未涉及下载或前端源码。用户配置、镜像与现有运行服务未修改。

## 配置账号与移除顶部栏追加验收 · 2026-10-01

管理账号改为必填的 YAML `auth.username`、`auth.password`，启动时生成内存中的 bcrypt 校验值；删除初始化码、设置密码接口、终端初始化提示以及 SQLite 密码读取/写入。旧库中已有的密码记录不参与认证，原有运行记录保留。缺失账号配置时启动报错，错误不包含密码。用户名支持中文，最多 128 字节且不允许首尾空白或控制字符；密码非空且最多 72 字节，示例采用用户指定字段与中文提示。

密码不参与配置的 JSON 编码，内部复制显式保留完整账号。配置读取 API 隐藏密码，页面保存其他设置不能覆盖或清空 `auth`，保留 YAML 注释。账号在启动时加载，修改 YAML 并重新载入会标记重启生效；重启前仍使用原账号，重启后旧账号和旧会话失效。登录保留统一错误、请求限流、同源校验和 HttpOnly / SameSite 会话 Cookie。侧栏显示实际登录用户名。

删除截图圈出的整条顶部栏及对应样式，包括面包屑、LOCAL MIRROR 和时区。手机使用独立按钮打开侧栏，选择页面后收起；已检查桌面和手机截图。

| 检查 | 本次实际结果 |
| --- | --- |
| Vue 类型检查与 Web 构建 | `npm --prefix web run build` 通过；真实 `web/dist/` 输出先构建，再嵌入测试二进制。 |
| Go 静态检查与竞态测试 | 使用仓库 GOCACHE 的 `go vet ./...`、带隔离 rsync 3.5.1 fixture 的 `go test -race ./...` 全部通过；app 包本次用时 34.365s。 |
| 账号与配置回归 | 覆盖空配置与长度/控制字符校验、中文账号、JSON 脱敏、浏览器保存保留账号和注释、非法重载保留当前值、旧 SQLite 密码不覆盖 YAML、账号重启生效与会话失效。 |
| 浏览器 E2E | 最终 **1 passed（测试 7.5s，总计 7.9s）**，无跳过。覆盖错误用户名、正确自定义账号、退出后再次登录、API 不返回密码、保存设置保留密码，以及原有真实 rsync 失败切换、发布、Range 下载和记录。 |
| 响应式页面 | 五页在 1440/1024/736/375 宽度均无顶部栏、无整页横向溢出、无页面脚本错误；375 宽度下验证导航打开、选择页面后侧栏实际移出视口。 |
| 本机与 Linux 构建 | 临时目录中的本机二进制、`CGO_ENABLED=0 GOOS=linux GOARCH=amd64` 交叉构建均通过；未替换现有二进制或服务。 |

首轮浏览器测试通过（8.8s），但手机截图捕获了侧栏收起动画中间状态；补充侧栏实际位置断言及最终重新登录后的脚本错误检查，再次执行通过后更新截图。截图位于 `../web/test-results/mirrordock-real-configurat-8d275-n-records-and-responsive-UI/`。

按 `use-modern-go` 的 Go 1.27.1 指南实施，使用 OCR 本地 delegate 的 preview/rule 逐文件审查本次 **11 个生产文件，11/11，跳过 0**，没有遗留确认的高/中严重度问题。OCR 默认排除的五项测试（`default_path`）与六项 Markdown 文档（`unsupported_ext`，含本节）另行检查。隔离审查副本以本轮修改前源码为基线，仅复制跟踪源码和新增测试/计划，没有复制用户配置、凭证或镜像。清单、规则与 SHA256 报告为 `../test-results/ocr-auth-preview.json`、`ocr-auth-rules.json`、`ocr-auth-review.json`。外部 LLM 调用未执行，沿用此前自动审批拒绝后允许的本地审查流程。

测试全部使用临时配置、目录、隔离端口和受控本地上游；用户配置、镜像数据及正在运行的服务未修改或重启。采用新程序前，现有 YAML 必须补上 `auth` 并设置自己的密码，账号与新界面在新二进制重启后生效。本次未重复 Docker、目标 Linux 运行、下载性能基准或实际发行版全量同步；下载热路径未修改，交叉编译不等同 Linux 运行验收。

## 旧二进制启动失败与程序日志追加验收 · 2026-10-01

已确认 `field auth not found in type config.Config` 的原因：现有 `bin/mirrordock` 与 Linux 二进制仍为 10:27–10:28 构建，上一轮账号功能只构建了临时测试程序。使用原本机二进制和临时空源 YAML 实际复现退出码 1、拒绝 auth，未创建数据目录。本次已将支持 auth 与 log 的最终程序重新生成到 `bin/mirrordock`、`bin/mirrordock-linux-amd64`、`bin/mirrordock-linux-arm64`；实际部署仍需替换所运行的程序并重启。

顶层 `log.level` 支持 debug/info/warn/error，默认 info；`log.format` 支持 json/text，默认 json；`log.path` 为空表示标准输出，填写时为相对于 YAML 的日志文件路径。父目录自动创建，文件以 0600 权限追加写入，不自动轮转，文档说明 logrotate copytruncate / 停机归档。先加载配置并初始化 slog，再初始化应用；HTTP 内部错误使用同一 logger，退出先完成资源关闭。配置错误或日志文件打开失败使用启动默认 JSON logger；已初始化之后的应用错误写入所选日志。日志修改后重启生效，回写保留注释及 auth 凭证。

程序日志不得进入镜像根、覆盖 YAML/SQLite/WAL/SHM/实例锁/TLS 文件；校验硬链接和符号链接别名。`<path>/logs/` 子目录为任务日志保留，程序日志可使用该目录根部的 `mirrordock.log`。FIFO/目录不接受为日志文件，打开时采用非阻塞标记避免 FIFO 无读者导致启动挂起。任务日志路径、格式及手动清理语义保持原样。

| 检查 | 本次实际结果 |
| --- | --- |
| Web 构建 | `npm --prefix web run build` 类型检查及 Vite 构建通过，先生成真实 web/dist 再编译 Go。前端更新配置类型和日志重启提示。 |
| Go 检查 | Go 1.27.1；使用仓库 GOCACHE 的 `go vet ./...` 通过；带隔离 rsync 3.5.1 fixture 的最终 `go test -race ./...` 通过，app 包 33.658s。最终配置/日志定向竞态测试通过。 |
| 配置与日志单元测试 | 覆盖缺省/局部 log 配置、非法字段/级别/格式、私有路径隔离、配置硬链接/符号链接别名、任务日志子目录、JSON/Text 内容、四级过滤、文件追加、权限、FIFO 及打开错误、回写注释和重启状态。 |
| 最终二进制 | 本机 `go build -trimpath -o bin/mirrordock ./cmd/mirrordock` 通过；CGO_ENABLED=0 的 Linux amd64/arm64 交叉构建通过，三项均已写入 bin。 |
| 隔离启动验证 | 最终本机程序验证默认 JSON、Text、文件 JSON（启动两次追加）、warn 过滤；均能以 YAML 账号登录，读取 API 无密码，管理页面与公开首页返回 200，SIGTERM 退出 0。配置保存保留密码并标记日志重启生效；日志初始化之后的应用错误写入 JSON 文件并退出 1。五项最终均通过。 |

完整竞态测试首次因沙箱禁止监听临时端口而失败，自动审批允许后重跑通过。启动验证脚本首轮遗漏 X-MirrorDock-Request 请求头，登录收到 403；补齐后正确。第二轮用于制造应用初始化错误的 fixture 实际先触发配置路径校验，没有生成文件日志；改用实例锁目录冲突，让错误发生在日志初始化后，最终通过。这些初次失败不记录为通过。实际输出和复现脚本在 `../test-results/log-go-race.txt`、`log-smoke.json`、`log-smoke.py`。

按 use-modern-go 的完整 Go 1.27 指南实施，沿用此前自动审批阻止外部 OCR LLM 后已允许的本地 delegate 工作流。本轮 `ocr review --preview` 初始无改动，未调用外部 LLM；实现后在只包含此次修改的隔离副本执行 `ocr delegate preview/rule`，当前 Codex 完成 **7/7 生产文件审查，跳过 0，覆盖率 100%**：cmd/mirrordock/main.go、configs/config.example.yaml、internal/app/app.go、internal/config/config.go、internal/observability/log.go、web/src/app/types.ts、web/src/features/settings/Settings.vue。默认排除的两项测试（default_path）和四项 Markdown（unsupported_ext）另行检查，本节验收说明也已核对。审查修复 FIFO 启动挂起、硬链接误写私有文件、与任务日志路径重叠三项问题，没有遗留确认的高/中严重度问题。清单、规则及 SHA256 结果见 `../test-results/ocr-log-preview.json`、`ocr-log-rules.json`、`ocr-log-review.json`；外部 LLM 调用未执行。

测试未修改用户配置、凭证、镜像或现有运行服务。下载热路径未修改，无新增逐块日志/数据库访问、哈希或压缩；性能基准本轮未重复。浏览器 E2E、Docker、目标 Linux 实际运行、sendfile syscall 与发行版同步未执行；交叉构建不替代目标平台验收。

## GitHub 代理排查与系统设置分类追加验收 · 2026-10-01

用户当前代理配置使用 HTTPS 协议，实际只读请求在 GitHub API 之前返回 `proxyconnect tcp: EOF`；同一端口改用 HTTP 诊断也返回 `unexpected EOF`，没有 CONNECT 响应。初次受限沙箱请求无法解析代理域名，不作为实际代理故障证据；经自动审批允许网络后复现上述断开。使用同一端口的 SOCKS5 则 TLS 成功、OpenList Release API HTTP 200，并成功下载校验一个 121 字节的真实 Release asset（ID 598014765，临时文件随后删除）。验证只覆盖小附件连接/完整性，不等同完整默认源同步或大文件吞吐验收。

按用户“2001 端口也行”的授权，通过当前本机服务的认证配置 API，将现有 proxy 的协议从 HTTPS 修正为 SOCKS5，保持原地址、2001 端口和 token；带 revision 检查、YAML 原子写回并应用到当前内存配置，下一次执行读取新代理。没有触发完整 OpenList/发行版同步，没有改动用户镜像文件或重启现有服务。诊断输出与文档不记录 token、完整代理 URL 或签名附件地址。

系统设置采用“同步执行 / rsync / GitHub Release”页签及左侧说明、右侧输入布局，沿用系统字体和蓝色主题，手机上下排列。监听/端口/数据目录/镜像根与 TLS 输入均移除；后端 Save 同样保留当前 YAML 的固定部署字段。外部编辑仍通过重新载入与重启应用，不恢复已删除的配置字段。Token/proxy 切换页签保留草稿，空输入保留现有值，显式清空时禁用输入。

GitHub 错误现在区分 DNS、连接拒绝、EOF、TLS、代理认证、GitHub 401/403/404 和两类限流；查询状态与失败摘要写入任务原始日志，不回显上游任意正文、凭证或 URL。补充 token 空白/控制字符校验。审查确认 SDK 的 auth transport 会在重定向时重新附加 token，因此认证 API 客户端限制同 origin；附件使用独立、不带 token 的客户端。下载热路径、流缓冲、数据库写入及发布逻辑未修改，没有新增逐块日志、整文件哈希或动态压缩。

| 检查 | 本次实际结果 |
| --- | --- |
| Go 指南 | 已读取 use-modern-go 的完整 Go 1.27 指南；工具链 Go 1.27.1，新 JSON 使用 encoding/json/v2。 |
| Web | `npm --prefix web run build` 类型检查与 Vite 构建通过，先生成真实 web/dist 后编译 Go。初次使用不存在的 Github 图标导出导致类型检查失败，替换为已支持的 ShieldCheck 后通过。 |
| 定向 Go 测试 | 配置与 sync 包通过。初次 sync 测试因沙箱禁止临时监听失败，经自动审批允许本地 fixture 后通过。覆盖四类代理的 API/附件下载、token/代理认证隔离、无直连回退、EOF 日志、固定错误诊断、认证/限流、取消错误脱敏、跨 origin 重定向拒绝及固定配置保存保护；原有续传/身份校验继续通过。 |
| 静态与竞态 | 仓库 GOCACHE 的最终 `go vet ./...`、带隔离 rsync 3.5.1 fixture 的 `go test -race ./...` 全部通过。首轮受到 test-results 内两个临时诊断 main.go 文件的包发现冲突影响；将诊断文本移为 .go.txt 后重跑通过，初次失败日志保留，未记为通过。业务包首轮实际全部通过，app 33.790s，sync 2.890s；最终命令复用通过结果缓存。 |
| 浏览器 | Chromium E2E **1 passed（测试 8.6s，总计 9.0s）**。覆盖页签初始状态、键盘切换、跨页签保存小数限速/Token/proxy、API 不泄露凭证、清空代理与保留账号/注释；固定部署输入不存在。五页在 1440/1024/736/375 宽度无整页横向溢出，系统设置全部页签分别检查；已查看桌面与手机截图，无脚本错误。 |
| 二进制 | 本机 bin/mirrordock 已重新构建；Linux amd64/arm64 为 CGO_ENABLED=0 交叉构建。新界面与新诊断需重启新二进制才能用于现有服务；已保存代理协议对下一次执行立即生效。 |

OCR 使用此前外部 LLM 被审批阻止后允许的本地 delegate preview/rule 工作流，本次未尝试外部 LLM。当前 Codex 已逐文件审查 **3/3 生产文件，跳过 0，覆盖率 100%**：internal/config/config.go、internal/sync/github.go、web/src/features/settings/Settings.vue。默认排除的 **4 个测试源码（default_path）**已另外全部审查：internal/config/config_test.go、internal/sync/github_test.go、internal/sync/proxy_test.go、web/e2e/mirrordock.spec.ts；所有新增/修改源码共 **7/7**。默认排除的五项 Markdown（unsupported_ext，包含本节验收）也已核对。已修复 token 重定向重新附加、包装取消错误携带 URL 的隐患，没有遗留确认的高/中严重度问题。清单、规则与 SHA256 覆盖结果见 ../test-results/ocr-github-settings-preview.json、ocr-github-settings-rules.json、ocr-github-settings-review.json；诊断脚本文本、最终测试输出及初次失败输出均保留在 test-results，均不提交。

性能基准本轮未重复：没有新的性能数据。Docker、Linux 目标机实际运行、sendfile syscall、大附件持续传输与完整发行版验收未执行；Linux 交叉构建不等同目标平台运行。回归测试仅使用临时 YAML、目录、端口、rsync daemon 与可控 HTTP/代理上游；唯一针对真实服务的配置写入为用户授权的代理协议修正。
