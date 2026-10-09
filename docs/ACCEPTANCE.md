# 验收记录

## 1.0 发布前最终验收（2026-10-09）

本节是本轮交付结论，后文保留历史记录，不代表当前工具链或当前扫描结果。检查包括全仓测试、核心运行/发布/鉴权/数据库/连接池/前端请求生命周期审查，以及本轮全部代码差异复核。应用版本已更新为 **1.0.0**，工具链按用户确认统一升级为 **Go 1.27.2**。本轮已识别的代码阻塞问题均已修复，下面列出的门禁通过或已完成明确告警判定。未提交、推送、打标签或发布。

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

本次按用户授权修复全部七项已确认问题，保留工作区已有改动，未提交、推送、部署、启动真实发行版同步、发送外部消息或使用子代理。配置字段没有新增或恢复；明确收紧来源/GPG URL 的凭据边界，完整代理管理端回填例外继续保留。实现方案见 [修复计划](plans/2026-10-03-review-fixes.md)。

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

## 2026-10-09：CLI 版本与构建注入

范围：cmd/mirrordock 的 CLI 拆分、版本显示与启动日志，scripts/build.sh、scripts/version.sh、Dockerfile 和 README。用户临时参考目录 video-rename/ 未修改，正式实现没有对它的依赖。

- Go 1.27.2 darwin/arm64；Web 构建通过。首次 Go 构建因沙箱禁止默认缓存写入失败，改用 GOCACHE="$PWD/.cache/go-build" 后完整构建通过，产物 bin/mirrordock。
- go test -race -count=1 ./cmd/mirrordock 与 go vet ./cmd/mirrordock 通过；sh -n scripts/build.sh scripts/version.sh 和 git diff --check 通过。
- 临时 Git 仓库实测：无 Git 为 dev、无 tag 为短 ID、精确轻量 tag 优先、离开 tag 后回退短 ID、嵌套源码归档不误用父仓库版本。
- 临时目录执行真实二进制 --version、--help，无配置即可成功退出；冲突配置别名退出码为 2。本机构建显示 a2e0f8cc1568 (modified)；链接注入 v1.2.3 实测显示 tag、revision 和 modified。
- OCR 外部自动审查已尝试，但因未配置 LLM endpoint 失败，未执行外部模型审查。随后使用 open-code-review-delegate 的本地 preview/rule，由当前 Codex 完成审查，未启动子代理、未发送源码到外部模型。
- OCR 清单 6/6 已审查：Dockerfile、main.go、cli.go、version.go、build.sh、version.sh。额外人工覆盖 cli_test.go、version_test.go（OCR 默认排除测试）及 README；video-rename/ 的 5 个参考文件明确排除。修复 Docker VERSION 链接参数引用方式，统一拒绝双引号，避免含单引号的 tag 被错误解析；无遗留正确性、安全性或性能阻断项。
- 未执行：Docker 镜像完整构建、Linux 运行验收、全套后端及浏览器 E2E（本次无业务/Web 源码变化）。参考目录仍包含其他模块的 import，删除前不使用 go test ./... 作为本次检查入口。没有执行真实同步或性能测试，未修改镜像数据与凭证。
