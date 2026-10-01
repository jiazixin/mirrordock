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

## 独立系统配置页与代理直接编辑 · 2026-10-01

已将系统设置底部的配置/运行环境区域移到独立侧栏页面 `/admin/system`。页面展示当前配置文件、实际生效的监听、数据目录/SQLite、镜像根、日志、TLS 路径，以及程序/Go/OS/架构/逻辑 CPU/启动时区；自动运行环境检查，将工具输出解析为版本和可用状态、目录权限及 GitHub 配置状态，不展示原始 JSON。固定部署字段取启动配置，重载 YAML 后继续展示旧的实际运行值并提示重启。检查失败会清除旧检查结果，显示失败和重试入口，避免把过期结果当成当前成功。

用户本次明确要求下载代理读取并直接修改，覆盖此前隐藏地址的界面/API 协议：受登录保护且 no-store 的配置 API 返回完整代理 URL，页面明文回填；保存和刷新后继续显示，输入清空并保存即直连。Token/管理员密码继续不返回，代理仍不进入运行快照、日志或审计；其他页面保存保留已有代理，revision/注释保留/原子 YAML 写回继续有效。前文旧版的“读取 API 隐藏代理、空输入保留”验收为历史记录，当前行为以本节为准。

| 检查 | 本次结果 |
| --- | --- |
| Web | 最终 `npm --prefix web run build` 通过，包括 Vue/TypeScript 类型检查；唯一输出为 web/dist。 |
| Go | Go 1.27.1，仓库内 GOCACHE 下 `go vet ./...` 通过；现代 rsync 3.5.1 隔离 fixture 的 `go test -race ./...` 全部通过。新测试确认 YAML 重载后的运行信息仍使用启动配置，路径为实际解析后的路径，运行信息不返回凭证。 |
| 浏览器 | 最终 Chromium E2E **1 passed（测试 10.5s，总计 10.9s）**。验证独立导航、解析后的环境字段/工具版本/可写状态、失败后清除旧结果与重试、代理明文读取/保存/刷新回填/清空直连、Token/密码隔离、注释保留及原有同步/发布/记录流程；六页在 1440/1024/736/375 宽度无整页横向溢出，没有脚本错误。已查看系统配置桌面和手机截图。 |
| 构建 | 最终 Web 输出之后构建本机 bin/mirrordock 与 /tmp/mirrordock-system-test，隔离 E2E 使用后者。现有服务未重启，新界面需运行新二进制后生效。 |

初次 Go 检查因系统构建缓存沙箱权限失败，改用仓库内 `.cache/go-build` 后 vet 通过。初次 race 测试因沙箱禁止本地 bind 失败，经审批允许隔离监听后执行；新增测试首轮因 macOS `/var` 到 `/private/var` 的真实路径解析导致预期路径断言失败，修正测试以已选配置文件的规范路径为基准，最终全量通过。这些失败不记为通过，初次监听失败输出保留于 `../test-results/system-go-test.txt`。

OCR 沿用项目此前外部 LLM 调用被审批阻止后允许的 local delegate preview/rule 流程，本次未执行任何外部 OCR LLM 调用、未发送源码。当前 Codex 已按 OCR 规则审查全部 **11/11 新增或修改源码**（8 个生产文件、3 个测试文件），另核对 YAML 和 5 项 Markdown。默认预览的 9 项（含 YAML）全部审查；默认排除的 3 项测试（default_path）已补充审查，5 项文档（unsupported_ext）已核对；总计 **17/17，跳过 0，覆盖 100%**。检查范围包含配置鉴权与脱敏例外、快照脱敏、原子写回、启动环境/重载语义、前端错误与重试、响应式布局、测试隔离。已修复环境检查失败后保留旧结果的问题，无遗留确认的高/中严重度问题。覆盖清单、规则和文件 SHA256 见 `../test-results/ocr-system-preview.json`、`ocr-system-rules.json`、`ocr-system-review.json`。

本轮未运行性能基准，没有新增吞吐数据；下载热路径、发布与缓冲实现未修改。Linux 实机、DNF reposync 插件/完整发行版、真实代理与 GitHub 网络连通性、sendfile 系统调用和生产吞吐未验证；环境页仅报告本机工具可执行性、写入权限及配置状态。测试只使用临时配置/目录、隔离端口、本地 rsync 与浏览器响应 fixture，没有修改用户配置、凭证或镜像，没有触发示例完整发行版同步。

## 公开目录文件列表 · 2026-10-01

按用户选中的文件管理参考图实施公开目录：浅灰背景、白色圆角表格、细分隔线、小尺寸线性图标、分段筛选与搜索。实际列为名称、类型、大小和修改时间，目录优先；显示面包屑与上级目录，支持名称/类型/大小/时间排序、当前已列出条目搜索、类型筛选及每页 50 项分页。目录、RPM/DEB、tar.gz/tar.xz/ZIP 等归档、JSON/XML/YAML、签名、校验和、ISO、文本和通用文件使用统一图标。手机保留名称和大小；未知虚拟目录大小/时间显示 `—`。

页面继续由 Go 渲染，HTML/CSS/JS 为 mirrors 模块自身源码；Vue 构建仍唯一输出 web/dist，不依赖外部字体、脚本或图标服务。目录最多读取/展示 1000 项，超过时明确搜索范围；lstat 不跟随符号链接、不回显目标。新增 8 条独立目录资源池，饱和返回 503/Retry-After；目录 HEAD 不读取条目、不占池。包和仓库元数据下载不占目录池；原有受限根、ServeContent/ReadFrom、Range 和 ETag 路径不变。HTML 模板转义路径与文件名，静态脚本以 CSP SHA-256 哈希授权；无 JavaScript 时仍显示全部已列出链接。

| 检查 | 本次结果 |
| --- | --- |
| Web / 构建 | `npm --prefix web run build`（Vue/TypeScript + Vite）和本机 Go 构建通过。新界面已构建到 bin/mirrordock，现有服务未重启。 |
| Go | Go 1.27.1，`go vet ./...` 和现代 rsync 3.5.1 隔离 fixture 的 `go test -race ./...` 全部通过。新增检查覆盖复合扩展名/仓库元数据/签名、真实大小/时间、符号链接目标隔离、隐藏条目、1000 项上限、转义与 CSP 实际脚本哈希、HEAD 无条目读取、目录池饱和时文件下载和 HEAD 继续可用。原有下载/发布回归通过。 |
| 浏览器 | Chromium E2E **2 passed（12.9s）**。公共目录新流程 3.2s，原系统配置/代理/同步流程 9.3s。无登录上下文验证根/虚拟父目录/实体目录、图标、筛选/搜索/空结果/清除、大小排序、55 项分页、真实文件下载、恶意文件名按文本显示、空目录和禁用 JavaScript 的 55 项链接。1440/1024/736/375 宽度无整页横向溢出、无脚本错误；已查看桌面和手机实际截图。 |
| 本机合成基准 | Apple M2 Max / macOS arm64，1000 个临时空 RPM 文件的目录读取+lstat+模板渲染：68 次，16,361,273 ns/op（约 16.36ms）、7,540,715 B/op、158,968 allocs/op。分配量包含 httptest.ResponseRecorder 保存完整 HTML 的成本，不能解释为生产峰值内存或网络吞吐；没有 Linux 性能结论。 |

结果文件为 `../test-results/directory-go-race.txt`、`directory-e2e.txt`、`directory-benchmark.txt`；截图在 `../web/test-results/public-directory-public-di-38c1e-ination-and-safe-file-links/`。初次无审批的本地下载测试被沙箱 bind 权限阻止，最终经审批以隔离监听运行；转义测试最初错误地编码整个请求路径导致 fixture URL 无效，已修正测试构造并通过。上述初次失败不算通过。

OCR 沿用项目授权的本地 delegate preview/rule 工作流，本轮未执行外部 OCR LLM 调用、未发送源码。当前 Codex 已逐项按规则审查当前工作区全部 **26/26 项，跳过 0，覆盖 100%**：默认 15 项（14 个生产源码和 YAML），默认排除的 5 个测试源码（default_path）均补充审查，6 项文档（unsupported_ext）均核对；新增/修改源码合计 **19/19**。范围包含此前系统配置/代理修改与本次 public listing 的 Go/HTML/CSS/JS、所有对应测试。审查补充了目录 HEAD 避免枚举、目录独立资源池及虚拟列表上限，修复后执行上述最终检查；无遗留确认的高/中严重度问题。文件清单、规则与 SHA256 覆盖见 `../test-results/ocr-directory-preview.json`、`ocr-directory-rules.json`、`ocr-directory-review.json`。

未执行 Linux 实机、生产磁盘目录基准、sendfile syscall、真实发行版验收或 Firefox/Safari 浏览器测试。本轮仅使用临时目录、隔离端口和可控本地 rsync，不修改用户镜像/凭证，不启动示例完整发行版同步；未重启部署服务、推送或发布。

## 文件图标改为 Phosphor Regular · 2026-10-01

按用户指定的 Phosphor Icons 截图替换文件列表图标：官方 Regular SVG、统一深灰、桌面 22px/手机 20px 的等比例尺寸，保留现有列表布局。目录、软件包、归档、元数据、签名、校验和、镜像、链接、文本及通用文件对应同系列图标；MD/PY/INI 使用带字母的文件图标，文件类型及筛选分类不变。固定官方 core 提交 `2b75f3ad12b420c9504ef05df8d2564a28f8500e`，13 个 SVG 对应 14 个类型映射；导入脚本校验几何元素、属性、颜色和模板标记，MIT 全文保留于嵌入模板。正常构建和页面无需联网获取图标。

- `npm --prefix web run build`、本机二进制构建、`go vet ./internal/mirrors`、相关列表测试及 `go test -race ./internal/mirrors` 通过；本轮没有重复其他未变模块的全量 race 检查，上节全量结果仍对应上节版本。
- 公共目录 Chromium E2E **1 passed（4.4s，测试 4.0s）**：现有搜索、筛选、排序、分页、文件名转义、公开下载及无脚本降级通过；1440/1024/736/375 布局无横向溢出、无脚本错误。已查看替换图标后的桌面/手机截图。
- 本机 1000 项目录合成基准：Apple M2 Max / macOS arm64，74 次、15,966,083 ns/op（约 15.97ms）、7,538,722 B/op、158,965 allocs/op。包含 ResponseRecorder 保留 HTML 的成本，不代表峰值内存、网络吞吐或 Linux 性能，也不据此声称性能提升。
- 官方图标首次读取因沙箱 DNS 权限失败，审批后读取；最初使用了不适用于仓库文件的 `file-regular.svg` 路径，404 后核实并改用仓库实际的 `assets/regular/<name>.svg`。正式导入完成，失败不计为通过。

OCR 使用项目授权的本地 delegate preview/rule，无外部 LLM 调用或源码发送。当前工作区清单 **28/28 项覆盖，跳过 0**（21 个实现/工具/测试源码、YAML、6 项文档）；本轮重新审查变更的 5 个源码（listing.go、listing.html、listing.css、listing-icons.html、导入脚本）及 5 项文档，其余 18 项 SHA256 与此前 26 项审查结果一致并继承审查记录。默认排除的测试/文档均在覆盖记录中补充说明；检查 SVG 外链/脚本/模板注入、模板信任边界、授权信息、离线构建、分类兼容及 CSS 尺寸/颜色，无遗留确认的高/中问题。清单、规则、哈希及继承依据见 `../test-results/ocr-phosphor-preview.json`、`ocr-phosphor-rules.json`、`ocr-phosphor-review.json`；测试/性能输出为 `phosphor-go-race.txt`、`phosphor-e2e.txt`、`phosphor-benchmark.txt`。

已构建 bin/mirrordock；现有服务未重启，新图标需运行新版二进制后生效。未执行 Linux 实机、sendfile syscall、生产镜像性能或 Firefox/Safari。测试仅使用临时目录、隔离本地端口和可控 rsync，没有修改用户镜像或凭证，没有推送或发布。

## 公开项目信息与页面精简 · 2026-10-01

按用户截图删除标题下说明、类型筛选和页脚，将搜索放到列表标题前方。普通镜像在实际 dest 项目行显示状态、大小、上次同步与下次计划；GitHub 仓库根行不加信息，仅在各 Release 行显示。各版本独立统计当前发布代的普通公开文件字节，成功校验后私有标记记录版本同步时间，未选版本保留旧时间，旧数据无记录时显示 `—`。状态与下次计划明确属于项目，时间悬停显示程序启动时区的准确时间。文件目录保留原有类型/大小/修改时间、Phosphor 图标、排序与分页；窄屏项目表格在面板内横向滚动。

摘要在发布阶段计算并保存在发布记录，旧代首次启动补算；每代最多 1000 个 Release 摘要，超出部分大小/时间未知。统计不跟随符号链接、不计私有文件/保留代/磁盘分配块。目录请求只读不可变摘要，源状态/cron 在目录查询中读取；每个 Release 列表只读取一次项目状态。普通文件、Range 和 HEAD 不进入任务数据库查询或递归统计，保留真实文件与 ReaderFrom 路径。

- Web 构建、`go vet ./...`、本机 Go 构建通过；已生成 `bin/mirrordock`，现有服务未重启。
- Go 1.27.1 / rsync 3.5.1 隔离上游的 `go test -race ./...` 全部通过；审查最终修复后列表/摘要相关 `go test -race ./internal/mirrors -run 'TestListing|TestDirectory|TestProject|TestOrdinary|TestSaturatedListing|TestSummary'` 通过。覆盖 GitHub 根行与版本行、普通项目大小、独立时间/未知记录、未选版本保留旧时间、重启恢复、标记保密、FIFO/越界链接标记忽略、CSP 与下载隔离。
- Chromium 公开目录 E2E 两项通过：临时 rsync 同步与 GitHub 已导入版本 fixture，验证状态、真实大小、cron 下次时间、程序时区、搜索、排序、55 项分页、清除搜索、空目录、无脚本降级、文件名转义与下载。检查 1440/1024/736/375px，页面无横向溢出、无脚本错误；已查看桌面/375px 的版本列表截图。GitHub 测试只用本地 fixture，不请求真实 GitHub。
- Apple M2 Max / macOS arm64 合成基准：8 MiB 公开文件回环 `BenchmarkPublicHTTP` 898 次、1,320,448 ns/op、6352.85 MB/s、10,762 B/op、142 allocs/op；1000 项目录读取/模板渲染 62 次、16,534,657 ns/op、8,388,367 B/op、176,969 allocs/op。包含 ResponseRecorder 保存 HTML 的分配，回环速度不代表生产吞吐或 Linux sendfile。

OCR 沿用此前自动审批阻止外部 LLM 后已允许的本地 delegate preview/rule；本轮没有执行外部 OCR LLM 调用或发送源码。当前 Codex 按规则完成全部 **12/12 新增或修改源码**（9 项生产源码、3 项默认 default_path 排除的测试补充审查），另检查 README、DESIGN、设计计划与本节；**16/16，跳过 0，覆盖 100%**。修复 Release 行重复读取项目状态、旧代摘要补算前根路径校验及私有标记可能阻塞读取的问题。没有遗留确认的高/中严重度问题。清单、规则、SHA256 和结构化报告为 `../test-results/ocr-project-listing-preview.json`、`ocr-project-listing-rules.json`、`ocr-project-listing-review.json`。

最初测试与基准因沙箱禁止本地 bind 失败，审批后使用隔离端口重跑通过；首轮新测试直接匹配带 `+08:00` 的 HTML 属性，忽略模板对 `+` 的转义，修正测试解码后通过，未改变模板转义。失败不记为通过。结果为 `../test-results/project-listing-go-race.txt`、`project-listing-go-final.txt`、`project-listing-e2e.txt`、`project-listing-e2e-final.txt`、`project-listing-benchmark.txt`；截图在 `../web/test-results/`。

未验证 Linux 实机/sendfile syscall、生产硬件吞吐、实际发行版、真实 GitHub/代理或 Firefox/Safari。旧 Release 同步时间无法从文件修改时间可靠恢复，下一次该版本成功同步后才有记录。测试没有修改用户配置、镜像与凭证，没有重启部署服务、创建子代理、推送或发布。

## 公开目录路径导航 · 2026-10-01

按用户最新截图移除品牌旁“镜像目录”、右上“公开下载”、重复的小面包屑、目录名尾斜杠及行尾操作列。大标题从小房子首页图标开始展示完整项目路径，各级链接可点击返回；根目录仅显示小房子，不显示 `/` 或“全部镜像”。文件名仍可进入目录或下载。首页链接有“首页”可访问名称，当前目录有 aria-current，路径保持模板转义与逐段 URL 编码。

验证范围为本轮五个源码（listing.go/html/css、listing_test.go、public-directory.spec.ts）：

- `npm --prefix web run build`、`go vet ./...`、Go 1.27.1 本机构建通过；生成 `bin/mirrordock` 与隔离测试二进制，现有服务未重启。Go 缓存使用仓库 `.cache/go-build`。
- `go test -race ./internal/mirrors` 通过，包含完整路径/特殊字符链接、首页/当前页语义、模板转义/CSP、下载与发布隔离回归。输出见 `../test-results/path-go-race-final.txt`。
- Chromium 两项公开目录 E2E 通过（5.1 秒），仅使用临时目录、随机端口和本地 rsync 3.5.1 上游/导入 fixture。覆盖小房子首页链接、完整多级路径、父路径点击、首页返回、无脚本跳转、原搜索/排序/分页与 GitHub 版本元数据；1440/1024/736/375px 无页面横向溢出、无脚本错误。已查看桌面及 375px 截图；输出 `../test-results/path-e2e.txt`，截图在 `../web/test-results/`。

OCR 沿用此前外部调用被自动审批阻止后的本地 delegate preview/rule，由当前 Codex 审查，未执行外部 LLM 调用或发送源码。隔离副本以本轮修改前工作区快照为基线；默认可审查 3 项生产文件全部完成，2 项测试（default_path）与 4 项文档（unsupported_ext，含本节）补充检查，**本轮 9/9，跳过 0**。当前工作区其余 7 个源码的 SHA256 与前次项目列表审查一致，继承其审查记录；总计 **12/12 新增/修改源码及 4 项文档覆盖**。无遗留确认的高/中严重度问题。清单/规则/哈希报告为 `../test-results/ocr-path-preview.json`、`ocr-path-rules.json`、`ocr-path-review.json`。

最初使用默认 Go 缓存被沙箱拒绝访问，改用仓库缓存；首次镜像测试被本地 bind 限制阻止，经自动审批允许后重跑通过，初次失败保存在 `../test-results/path-go-race.txt`，不计为通过。本轮未新增性能基准（仅列表展示/导航改动，文件传输路径未变）；前次合成性能数据不作为本轮结果。未验证 Linux 实机/sendfile、生产吞吐、实际发行版、真实 GitHub/代理、Firefox/Safari。没有修改用户配置、镜像或凭证，没有重启部署服务、创建子代理、推送或发布。

## 目录 Header 局部改造 · 2026-10-01

按本次完整规格将公开目录顶部改为白底三层 Header：“当前位置”、动态 Home/Chevron 面包屑、列表标题与普通数量/右侧搜索。祖先目录复用原安全编码链接，当前目录为不可点击的 aria-current 文本；彻底移除上级目录按钮、样式及不用的 Parent 字段。复用字体与单色线性图标；搜索 320×38px，800px 以下另起一行铺满容器。标签至路径 6px、路径至工具栏 22px、工具栏至表格 14px，Header 与原表格 Card 严格对齐。长路径只在导航内部滚动，脚本首次显示及窗口缩放时定位当前目录，超长段落省略且保留完整悬停文本；无脚本仍有全部原生父路径链接。

本轮修改 6 个源码：listing.html/css/js/go、listing_test.go 和 public-directory.spec.ts。搜索过滤/输入事件、表格主体/字段/状态 Badge/行高、排序/分页、接口、同步和下载路径保持现状。

- `use-modern-go` 读取 Go 1.27.1 完整指南；`npm --prefix web run build`（Vue 类型检查/Vite）、`GOCACHE=$PWD/.cache/go-build go vet ./...`、本机 Go 构建通过，已生成 bin/mirrordock 及 /tmp/mirrordock-header-test。现有服务未重启，新 Header 需重启运行新版二进制后生效。
- `GOCACHE=$PWD/.cache/go-build MIRRORDOCK_TEST_RSYNC=$PWD/.cache/rsync/rsync-3.5.1/rsync go test -race ./internal/mirrors` 最终通过，覆盖根/祖先/当前目录语义、特殊字符模板转义、CSP 实际脚本哈希、目录容量/HEAD、版本摘要及文件下载/发布回归。结果：`../test-results/header-go-race-final.txt`。
- `MIRRORDOCK_TEST_BINARY=/tmp/mirrordock-header-test MIRRORDOCK_TEST_RSYNC=$PWD/.cache/rsync/rsync-3.5.1/rsync npm --prefix web run test:e2e -- public-directory.spec.ts` 最终 **2 passed（5.5s）**。覆盖每级链接、Home 返回、当前不可点击、取消上级按钮、9 级深路径/超长名称/空格中文和 &、无脚本导航、1440/1024/736/375/320px、Header 与表格边界/14px 间距、当前段可见、搜索右侧/窄屏铺满及搜索/清除/排序/55 项分页/下载/版本元数据。无整页横向溢出、无脚本错误，已查看桌面、375px 列表和 320px 深路径截图。结果 `../test-results/header-e2e-final.txt`，截图 `../web/test-results/`。
- 初轮 mirrors race 未通过：已有测试仍要求搜索在列表标题前方，与本次指定标题左侧/搜索右侧顺序相反；更新该布局断言后最终通过，没有改动数据/下载断言。首次失败保留在 `../test-results/header-go-race.txt`，不记为通过。

OCR 沿用此前外部调用被审批阻止后允许的 local delegate preview/rule，由当前 Codex 按规则审查；本次未执行外部 LLM 调用、未发送源码。本轮 6/6 源码与 4/4 文档重新检查，其余 6 个已改源码 SHA256 与 `ocr-path-review.json` 一致并继承审查记录；当前工作区全部 **12/12 新增/修改源码、4/4 文档覆盖，16/16，跳过 0**。默认 9 项源码全部覆盖（4 项本轮、5 项继承），3 个默认 default_path 排除的测试补充检查（2 项本轮、1 项继承），4 项 unsupported_ext 文档核对。核查原生导航/不可点击当前页、转义/CSP、搜索逻辑保持、布局边界、隔离 fixture 与清理、文件下载热路径不变；无遗留确认的高/中严重度问题。清单、规则和哈希报告：`../test-results/ocr-header-preview.json`、`ocr-header-rules.json`、`ocr-header-review.json`。审查后执行上述最终相关测试。

本轮没有新增性能基准，先前合成吞吐不作为本轮结果；未验证 Linux 实机/sendfile syscall、生产性能、实际发行版、真实 GitHub/代理及 Firefox/Safari。只使用临时目录、随机本地端口、可控 rsync/本地已导入版本，不修改用户镜像/凭证，不启动完整发行版同步，未创建子代理、重启部署、推送或发布。

## 管理页面精简与设置合并 · 2026-10-01

按截图删除侧栏品牌副标题、连接/版本状态栏和所有管理页标题下说明；同时删除批次预算说明、计划管理的调度时区提示整行及同步源页底发布说明。侧栏系统配置入口和独立页面已移除，系统设置在 GitHub Release 后只有一个“系统配置”页签，内含配置文件路径和环境检查。环境检查进入时读取，点击“刷新”重新请求，刷新期间禁止重复点击；失败清除旧结果并显示错误，可再次刷新恢复。配置文件/检查结果使用 Vue 文本插值，原设置草稿、保存与凭证行为保留。

- Vue 类型检查和 Vite 构建、Go 1.27.1 本机嵌入构建、`GOCACHE=$PWD/.cache/go-build go vet ./...`、`git diff --check` 通过。已生成 `bin/mirrordock` 与 `/tmp/mirrordock-admin-cleanup-test`，现有服务未重启。
- Chromium 管理 E2E 最终 **1 passed（9.2s）**，使用临时 YAML/镜像目录、随机端口和本地 rsync 3.5.1。覆盖新增源、来源切换、发布/Range、运行记录、代理和 Token 保存回归、四个页签顺序/键盘切换、环境检查重读后的不同结果、503 错误和再次刷新恢复。五个管理页面及全部设置页签在 1440/1024/736/375px 无整页横向溢出、无页面脚本错误；截图等待检查完成，已查看桌面/375px 完整检查结果。结果 `../test-results/admin-cleanup-e2e-final.txt`；截图 `../web/test-results/mirrordock-real-configurat-8d275-n-records-and-responsive-UI/`。
- 最初 Chromium 启动被沙箱 MachPort 权限阻止，未执行测试；经自动审批允许隔离浏览器/本地监听后完成上述验证。初次失败保存在 `../test-results/admin-cleanup-e2e.txt`，不记为通过。

OCR 沿用此前外部调用被审批阻止后的本地 delegate preview/rule 工作流，由当前 Codex 完成审查，未执行外部 LLM 调用、未发送源码。当前工作区 **27/27 项覆盖，跳过 0**：本轮 14 项（10 个源码/测试、删除旧页面、3 项文档）重新检查，13 项 SHA256 与 `ocr-header-review.json` 一致并继承审查记录。默认排除的测试（default_path）、文档（unsupported_ext）和删除文件均显式记录。源码合计 23/23（含删除旧页面），无遗留确认的高/中严重度问题。检查页签装配/键盘切换、异步错误和 finally、重复请求、文本转义/凭证、窄屏和保存回归；最终相关 E2E 在审查后执行。清单、规则、哈希覆盖为 `../test-results/ocr-admin-cleanup-preview.json`、`ocr-admin-cleanup-rules.json`、`ocr-admin-cleanup-review.json`。

本轮没有 Go 源码改动或新增性能基准；未重复全量 Go race，未验证 Linux 实机/sendfile、生产性能、实际发行版、真实 GitHub/代理及 Firefox/Safari。没有修改用户镜像/配置/凭证，没有启动示例完整同步、创建子代理、重启部署、推送或发布。

## 同步源拆列与标签样式 · 2026-10-01

同步源表格拆成“同步源 / 来源 / 目标 / 最近运行 / 状态 / 操作”，移除镜像发布列及复制按钮；来源保留备用数量和完整 title，目标独立显示，最近运行沿用创建时间，未运行状态使用灰色标签。类型标签统一蓝色 rsync、橙色 reposync、紫色 GitHub Release；按用户随后提供的参考图，类型/状态统一使用 6px 圆角、语义细边框、深文字/浅背景标签，成功绿、运行蓝、排队灰蓝、暂停琥珀、失败红、超时橙、中断紫、取消/未运行灰。批次取消外围嵌套框及独立卡片边框，采用左侧时间/圆点/连续竖线，批次编号加粗；统计框使用同一 6px 圆角和细边框，添加圆点，完成绿色、跳过琥珀、错误红色，标题可换行；同步源图标和类型颜色共用变量。视觉规范更新于 DESIGN 第 8 节。

- `npm --prefix web run build`（Vue 类型检查 / Vite）及 Go 1.27.1 嵌入构建通过，已生成 `bin/mirrordock` 与 `/tmp/mirrordock-admin-tags-test`；`git diff --check` 通过。现有服务未重启，新界面在重启新版二进制后生效。
- Chromium 管理 E2E 最终 **1 passed（11.3s）**，日志 `../test-results/admin-tags-reference-e2e-final.txt`。在临时 YAML/镜像目录、随机端口、本地 rsync 3.5.1 上游验证新增/编辑、来源切换、发布与 Range、运行记录、设置保存/Token、环境检查和键盘回归；新增拆列/无运行状态断言。三种类型、九种运行状态使用只读浏览器响应 fixture 展示，不执行 reposync 或真实 GitHub 同步。同步源/批次在 1440/1024/736/375/320px 无整页横向溢出，无页面脚本错误；已查看最终桌面与 375px 截图，截图在 `../web/test-results/mirrordock-real-configurat-8d275-n-records-and-responsive-UI/`（sources-tags、batches-tags、batches-tags-mobile）。
- 初次 Chromium 启动被沙箱 MachPort 权限阻止，未执行测试；自动审批允许隔离浏览器后测试通过，失败日志为 `../test-results/admin-tags-e2e.txt`，不计为通过。首次截图发现自动表格布局挤窄目标列，改为明确列宽并增加目标空间，最终重新构建/审查并运行上述测试。用户在实施中提供新的标签/时间线参考图后，取消初版胶囊和独立卡片，按新图统一小圆角细边框及连续时间线；重新构建、审查和运行最终 E2E，已检查桌面/手机截图。

OCR 沿用此前外部调用被审批阻止后的本地 delegate preview/rule，由当前 Codex 完成审查；本轮未执行外部 LLM 调用，未发送源码。当前工作区 **28/28 项覆盖，跳过 0**：8 项重新检查（StatusBadge、Sources、Batches、global.css、管理 E2E、README、DESIGN、本节），其余 20 项 SHA256 与 `ocr-admin-cleanup-review.json` 一致，继承其审查记录；源码合计 **24/24（含删除旧页面）**、文档 4/4。默认排除的测试（default_path）、文档（unsupported_ext）和删除项明确记录，检查列/空态跨度、Vue 转义、删除发布操作后的引用、样式覆盖/可读性、窄屏和隔离 fixture，无遗留确认的高/中问题。报告为 `../test-results/ocr-admin-tags-preview.json`、`ocr-admin-tags-rules.json`、`ocr-admin-tags-reference-rules.json`、`ocr-admin-tags-review.json`。最终源码审查后执行上述 E2E。

本轮仅修改管理前端/测试及文档，没有新增性能基准，不把历史合成性能当作本轮数据；没有 Go 源码改动，未重复 Go race/vet。未验证 Firefox/Safari、Linux 实机/sendfile、生产性能、实际发行版与真实 GitHub/代理。没有修改用户镜像/配置/凭证，没有启动示例完整同步、创建子代理、重启部署、推送或发布。

## 2026-10-01：表格中轴对齐与紧凑时间

按用户参考图将“全部同步方式”改为“同步方式”，桌面筛选框由 190px 缩为 150px；最近运行改为日期/时分秒上下两行、共享中轴，列宽由 170px 缩为 130px。时间仍通过原有程序启动时区格式化，未运行只显示一个 `—`；运行记录复用同一展示组件。管理表格和公开目录表头文字居中；状态、时间、数值居中，名称/来源/目录与记录原因保留左对齐，操作及详情按钮靠右。公开排序箭头绝对定位到文字右侧，不参与居中计算，保留原有排序交互和 aria-sort。

验证与边界：

- Vue 类型检查/Vite 生产构建、Go 1.27.1 嵌入构建及 `git diff --check` 通过。已构建 `bin/mirrordock` 和隔离测试二进制 `/tmp/mirrordock-alignment-test`；现有服务未重启，需运行新版二进制后生效。
- 审查后的最终 Chromium E2E **3 passed（14.8s）**，覆盖管理与公开目录原有配置/同步/发布/下载/排序/分页/窄屏回归。结果 `../test-results/alignment-e2e-final.txt`；已查看同步源、公开文件列表的桌面截图，以及管理/公开目录手机截图。
- 额外临时浏览器测量验证筛选框实际占宽为 150px，全部表头居中；日期、时分秒、状态与列中轴误差不超过 0.01px，公开表头文字中心误差不超过 0.5px，箭头均在文字右侧。操作按钮无溢出且靠右；1440/1024/736/375/320px 无整页横向溢出。程序使用 Asia/Shanghai、浏览器使用 America/New_York，固定 UTC 07:13:56 仍显示 15:13:56。测量结果 `../test-results/alignment-geometry.json`，临时诊断脚本 `alignment-geometry.cjs`，未新增仅复述样式实现的持久测试。
- 使用仓库 Go 缓存和现代 rsync 隔离 fixture 的 `go test -race ./internal/mirrors` 通过，结果 `../test-results/alignment-go-race-final.txt`。首次受沙箱限制，Chromium Mach port 启动和本地 TCP 绑定失败，尚未进入功能检查；获准在沙箱外重跑隔离测试后通过，初次失败日志分别保存为 `alignment-e2e.txt`、`alignment-go-race.txt`，不记为通过。

OCR 沿用此前外部调用被自动审批阻止后允许的本地 delegate preview/rule，由当前 Codex 按规则审查，未执行外部 LLM 调用、未发送源码。本轮 **6 个源码及 3 项文档重新审查**，其余 **20 项 SHA256 与 ocr-admin-tags-review.json 一致并继承审查记录**；当前工作区 **29/29 项覆盖、跳过 0**，包括 **25/25 源码（含删除旧页面）及 4/4 文档**。默认排除的 4 个测试（default_path）、4 项文档（unsupported_ext）和删除页面均记录覆盖依据。已修正公共组件反向依赖 app、筛选宽度未作用于实际容器、操作列缩窄导致按钮空间不足及左对齐覆盖空态的问题；无遗留确认的高/中严重度问题。清单、规则、哈希与结构化审查报告见 `../test-results/ocr-alignment-preview.json`、`ocr-alignment-rules.json`、`ocr-alignment-review.json`；最终 E2E 与位置测量在源码审查后执行。

本轮没有新增性能基准，未验证 Linux 实机/sendfile syscall、生产吞吐、实际发行版或 Firefox/Safari；文件下载热路径未改变。测试仅使用临时配置/目录、随机端口、本地 rsync 与只读浏览器响应 fixture，未修改用户镜像和凭证，未启动案例全量同步、创建子代理、重启部署服务、推送或发布。

## 2026-10-01：修正表头、下拉及计划时间

用户反馈后更新：同步源前三列“同步源/来源/目标”表头恢复左对齐；运行记录的同步源和处理结果/失败原因表头左对齐。详情标题使用与按钮同宽的 32px 区域，共用右侧中轴。自定义选择菜单取消 180px 最小宽度，菜单随触发框同宽并继续受视口限制。计划管理同步源表头左对齐，“下次运行”复用上下两行时间，日期 600 字重/深色，时分秒浅色，程序启动时区不变；未启用计划保持“未启用”。

- Vue 类型检查/Vite 构建、Go 1.27.1 嵌入构建（bin/mirrordock 与隔离测试二进制）、git diff --check 通过；现有服务未重启。
- 最终管理 Chromium E2E **1 passed（9.9s）**，日志 `../test-results/alignment-fix-e2e-final.txt`。配置/运行/设置/键盘/窄屏回归使用临时目录、随机端口与本地 rsync fixture。
- 临时测量通过：150px 桌面筛选框与菜单同宽、左边界一致，1440/1024/736/375/320px 均同宽且无整页横向溢出；记录详情文字/按钮中心偏差为 0px。计划日期/时间中心偏差为 0px、确认为上下行和 600 日期字重，固定 UTC 时间在纽约浏览器仍按上海启动时区显示 15:13:56；停用计划不展示时间。已查看同步源展开菜单、运行记录和计划管理的最终桌面截图。结果 `../test-results/alignment-fix-geometry.json`，截图 `alignment-fix-sources.png`、`alignment-fix-records.png`、`alignment-fix-schedules.png`；诊断脚本保存在忽略的 test-results 中。

OCR 沿用已允许的本地 delegate preview/rule，由当前 Codex 完成审查，无外部 LLM 调用或源码发送。本轮 4 个源码（SelectField、global.css、Records、Schedules）和 3 项文档重新审查，其余 23 项 SHA256 与 ocr-alignment-review.json 完全一致并继承审查；当前工作区 **30/30 项覆盖、跳过 0**，源码 **26/26（含删除页面）**、文档 4/4。默认排除的测试/文档/删除项保留明确覆盖依据。核查选择框视口限制、键盘/菜单定位保持、时间脱敏与启动时区、停用/缺失时间分支、共享组件依赖边界和最终 CSS 优先级，没有遗留确认的高/中严重度问题。报告 `../test-results/ocr-alignment-fix-preview.json`、`ocr-alignment-fix-rules.json`、`ocr-alignment-fix-review.json`。最终 E2E 和位置测量在源码审查后执行。

本轮为管理界面调整，未重复 Go 竞态/vet 和性能基准，没有新增性能数据；未验证 Firefox/Safari、Linux 实机/sendfile、生产吞吐或实际发行版同步。未修改用户配置、镜像或凭证，未启动完整发行版同步、创建子代理、重启部署、推送或发布。

## 2026-10-01：通知设置与持久发送队列

保留本轮开始前的全部工作区改动。在系统设置新增通知页签，五渠道默认关闭，横向/窄屏单列卡片、可访问帮助、独立保存/测试反馈与敏感输入清空；底部最近 30 条记录每 5 秒刷新，离开页签停止轮询。YAML 专用配置保存保留注释并校验 revision，通知凭据 AES-256-GCM 加密，读取只返回存在标记。项目原本没有加密密钥管理，新增数据目录 `notification.key`，已说明和 YAML/SQLite 一起备份。

SQLite 新增终态事实快照与通知队列，终态事实与任务结果同事务保存，修复中断后快速继续导致扫描遗漏的问题；通知内容生成后固定正文/时区，启用边界按任务 ID 排除历史及启用前创建的活动任务。独立 worker 扫描去重、失败过滤、关闭停止、三次尝试及 30/60 秒退避，启动恢复未确认发送态。每轮生成最多每渠道 100 项、发送 10 项；HTTP 在配置锁外发送，下载热路径未接入通知。当前每个任务只有一个源/目标，不新增跨任务聚合；超时归类任务失败，首次终态通知后继续同编号不再重复入队。

验证均使用临时配置、数据库、镜像目录、本机随机端口、可控 rsync daemon 和模拟通知接收端，未向真实渠道发送测试消息：

- `npm --prefix web run build`：通过，含 vue-tsc 类型检查；输出仅 `web/dist/`。
- `MIRRORDOCK_TEST_RSYNC=<隔离 rsync 3.5.1> GOCACHE=<项目缓存> go test -race ./...`：通过，包含现有源同步/发布/鉴权等回归。最终审查修正后另运行通知、runs、app、database 的相关 race 测试；结果见 `test-results/notification-final-race.txt`。
- `go vet ./...`：通过；macOS 构建与 `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build`：通过，测试二进制位于 /tmp，未替换实际运行服务。
- 全部 Chromium E2E：**4/4 通过、跳过 0**，包含原有管理台和公开目录回归。通知页覆盖五渠道默认状态、凭据不回显、空值保留、钉钉清除密钥、保存后清空、未保存禁测试、关闭自动通知时测试、请求期间两按钮禁用、启用不补旧任务、新任务发送、时区同步、发送记录、帮助悬停/聚焦/点击/Escape、1440/736/375px 无横向页面溢出，以及离开后停止轮询；截图已人工核对，位于 web/test-results。
- Go 通知测试覆盖：非法渠道/URL/官方参数/Token/缺失配置、revision 冲突、通用设置保留通知、Cookie/权限/同源/CSRF、密文渠道/字段绑定与篡改、错误/缺失密钥和权限、启用/重新启用边界、关闭/仅失败停止、成功/取消/含失败条目过滤、队列唯一去重、30/60 秒延迟与三次上限、真实关闭/重新打开 SQLite 后恢复、未确认发送达到上限失败、快速继续仍保留中断事件、UTC 默认/IANA 时区与夏令时偏移及正文快照、名称截断/未开始/未记录/未完成数、Bark 末尾斜杠/自建路径前缀、五渠道本机请求与业务响应、钉钉签名、64 KiB 上限、禁止重定向、超时和错误脱敏、并发测试拒绝、30 条查询上限及通知失败不修改业务结果。

下载合成回归基准（Apple M2 Max、darwin/arm64、Go 1.27.1）：BenchmarkPublicHTTP **1,332,072 ns/op，6297.41 MB/s，10805 B/op，145 allocs/op**。仅为本机临时文件/回环基准，不是 Linux sendfile 系统调用观测或生产网络/磁盘吞吐验收。

OCR 首次 `ocr review --audience agent ... --output` 在解析 endpoint 阶段失败：未配置 LLM endpoint，因此未执行外部 LLM 请求或发送源码。按照项目已允许的本地 delegate preview/rule 工作流，由当前 Codex 逐项审查；默认可审查 **33/33**，默认排除的 7 个测试（default_path）补充审查、5 个 Markdown（unsupported_ext）核对、删除旧页面检查装配引用，共 **46/46 项、跳过 0、覆盖 100%**。其中新增/修改源码 **39/39**，另覆盖删除源码 1 项、YAML 1 项和文档 5 项；未变化文件仅在 SHA256 与此前 ocr-alignment-fix-review.json 完全相同时继承审查。当前源码检查内容包括锁顺序/领取与停用、复用配置保护、事务终态事件、正文计数/时间、固定脱敏错误、HTTP 限制、凭据 AES-GCM/密钥权限与备份、Vue 转义/交互/轮询、测试隔离。修复快速继续丢失事件、Bark isArchive 参数类型、未报告结束时间不伪造完成时间及模拟超时接收端无界等待问题，无遗留确认的高/中严重度问题。

初次浏览器断言因 Chromium 返回 Asia/Calcutta（Asia/Kolkata 的 IANA 别名）失败，已按浏览器实际同步名称和 UTC+05:30 偏移验证。初次额外超时 fixture 一直等待关闭导致测试挂起，已终止该自有测试进程、增加 200 ms 等待上限并重跑；这些检查不记为通过。初始沙箱阻止本机监听及默认 Go 缓存写入，改为项目 GOCACHE 并获准执行隔离端口测试。

报告与命令输出：test-results/notification-ocr-preview.json、notification-ocr-rules.json、notification-ocr-review.json、notification-web-build.txt、notification-go-race.txt、notification-final-race.txt、notification-go-vet.txt、notification-linux-build.txt、notification-e2e.txt、notification-download-benchmark.txt；报告及生成物均忽略。配置方法、触发、重试/可能重复语义和凭据备份要求已更新 README 与配置案例。

未验证：真实企业微信/钉钉/Telegram/Bark 接收和平台实际限制、Linux 实际启动/生产负载/发行版验收与 sendfile 系统调用、缺少系统时区文件的实际容器运行（已嵌入 tzdata 并完成 Linux 构建）、外部 OCR LLM 审查。没有推送、发布或外部消息发送。
