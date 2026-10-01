# 实现说明

MirrorDock 使用 Go 1.27.1、Vue 3 / TypeScript。配置字段沿用用户确认的 schema，完整注释见 `configs/config.example.yaml`。

## 模块与数据

- `internal/config`：严格 YAML 校验、srcN 数字排序、路径隔离、注释保留、revision 冲突、0600 原子写回和 TLS 配对验证。
- `internal/database`：纯 Go SQLite、WAL、同步等级 FULL、短事务。数据库及表、索引在启动时自动初始化，建表语句直接内置在该包中，重复打开保留已有数据。运行主记录以带结构化字段的 JSON 存储，attempts/events/快照聚合在一条运行记录内；发布指针单独存储，数据库不再参与管理员密码认证。当前没有另建设计中建议的每种明细表。
- `internal/sync`：系统 rsync 的参数、CR/LF 进度、进程组停止；DNF 4 私有 repo 配置；go-github v88 与流式附件下载、有效 Range 续传、身份侧车文件、可用 SHA-256 校验。不请求自动源码归档。
- `internal/runs`：两个内置 worker、同源排他、五字段 cron、8h 默认累计预算、暂停/取消/继续、重启恢复和日志脱敏。预算覆盖准备到发布，排队和暂停不计入。异常断电最多丢失一个 5 秒进度检查点；正常关闭会保存实际执行时间。
- `internal/mirrors`：私有工作目录、reflink 优先、独立复制回退、RPM/APT 索引及包大小校验、原子持久发布指针、旧代读取与 24 小时回收保护。
- `internal/observability`：根据顶层 log 构造 slog JSON/Text logger，info/标准输出/json 为默认，文件相对配置目录追加写入；启动前初始化，退出时关闭，HTTP 内部错误共用 logger。修改后重启生效，不改变任务日志及下载热路径。
- `internal/app`：启动时加载 YAML 的 auth.username/auth.password，内存 bcrypt 校验值、12 小时 Cookie 会话、登录限流与同源写请求验证；管理 API、SSE、嵌入页面、HTTP/TLS 和连接无进展检测。无初始化码及设置密码 API，账号修改后重启生效。

目录布局依据根 `目录规范.md`，没有 `backend/`、额外根 Node 清单或第二份 Web 编译输出。

## 下载性能

普通文件和单段 Range 保留真实 `*os.File` 和 `io.LimitedReader`，计数 ResponseWriter 实现并委托 `ReadFrom`。Go 标准库可继续选择 Linux HTTP/1.1 sendfile；TLS/HTTP2 使用标准库流式传输。下载热路径不读写 SQLite、YAML或正文日志，不整文件哈希，不动态压缩；ETag 对不可变代、路径、大小和时间生成，有界 LRU 8192 项。

大文件下载池 256 条流、256 KiB 以下文件另有 64 条资源池、SSE 8 条、管理会话 32 条，TCP 连接按文件描述符限制预留资源并最多 4096；目录最多列出 1000 项。下载期间只有短路由引用锁，不持同步任务锁。每个请求固定发布根，引用释放后关闭旧根，旧代到期且无引用后才清理。大文件活动流超过 75% 内置容量时暂缓启动下一同步任务和旧代回收，已运行任务继续执行。同步进程在 Linux 尝试 nice=10 和 best-effort I/O priority=7，不改变下载连接。

无进展监测每 30 秒读取一次活动 TCP 的内核统计。Linux 使用已确认字节、macOS 使用非重传发送字节；整个连接 5 分钟无进展才关闭，正常有进展的大文件不受固定总时长限制。HTTP/2 同连接有任一流持续传输时不会误关整条连接。这个机制不替换 TCP 连接或文件 Reader，因此不破坏 sendfile；不支持 socket 统计的平台不会假装具备该监测能力。

RPM metadata 支持 gzip/bzip2/xz/zstd，压缩索引流式读取，校验已声明的元数据 SHA-256/SHA-512 和包大小；APT 校验 Release 的 SHA-256 条目并流式读取 Packages。不会每次校验全仓所有软件包的内容哈希，客户端签名验证仍适用。DNF 签名与真实发行版安装应在目标 EL 上继续验收。

## 发布和恢复

工作目录是 `<mirror_root>/.mirrordock/work/<run-id>`，发布代是 `.mirrordock/published/<uuid>`。准备标记只在复制全部完成后写入；准备阶段暂停后会继续未完成复制。成功校验并 fsync 文件和目录后密封代、写 manifest、预验证公开链接与文件根，再提交 SQLite 指针并替换内存路由；提交失败时回滚链接和暂存。服务重启依据数据库修复链接；没有被指针引用的暂存/孤立代不会对外提供。

新任务基于当前代复制，失败保留本轮暂存供检查；继续沿用相同 run-id 和预算。旧代自被替换起至少保留 24 小时，当前路径缺失的旧包可以回退读取，以照顾仍持有旧 metadata 的客户端。原始已存在的 dest 目录在首次导入校验后会移动到私有发布代，并以同路径链接替代；部署后不要直接修改已发布代。

GitHub 的 `settings.github.proxy` 使用 Go 原生 HTTP/HTTPS/SOCKS5 代理，API 查询和资产下载共享本轮的连接池；每轮结束关闭空闲连接。空值直连，不使用代理环境变量，也不在故障时自动直连。代理认证遵循 URL userinfo，TLS 使用标准证书校验。代理与 Token 在每次实际执行时从当前配置读取，持久快照不存储二者。

配置读取 API 不返回 Token 或完整代理地址。写配置时 `token_update=null` 保留已有 Token，空字符串显式清空；`proxy_update` 同样以 null 保留、空字符串清空；其他配置都带 revision。通过 UI 删除源只取消公开路由和计划，不删除既有镜像文件。删除/改变活动、暂停或中断任务的 dest 必须先取消该任务。

## 实际范围

运行记录当前按任务聚合，成功/失败页分页查询运行，详情显示 attempts、事件、日志和脱敏配置快照。GitHub 的资产完成事件保留在原始日志，尚未提供每资产的独立数据库检索页。reposync 的详细输出可查看日志，进度总量无法可靠取得时显示未知，不伪造百分比。

参考图的布局已落实在 Vue 五个页面，采用钴蓝新 Logo 和本地 SVG 图标，不依赖远程字体/CDN。现版采用浅色主题。生产 Linux sendfile 系统调用、HTTPS/HTTP2 及完整 EL 包签名同步的实测边界以 `ACCEPTANCE.md` 为准。

系统设置分为同步执行、rsync、GitHub Release 三个页签，采用左侧说明、右侧输入布局，窄屏上下排列。监听、端口、数据库/日志目录、镜像根及 TLS 无页面输入，Config.Manager.Save 保留当前 YAML 中这些固定值；外部编辑仍由 revision 冲突保护，重新载入后需要重启。凭证字段切换页签保留草稿，保存后清空输入；显式清空时禁用对应输入。

GitHub API 与下载网络失败使用固定诊断消息，区分 DNS、TLS 证书、连接拒绝、EOF、代理认证及 GitHub 401/403/404/限流，不回显底层 URL 或上游任意正文。扫描和查询状态、失败摘要写入任务原始日志，未新增逐块日志；取消/截止错误保留 context sentinel，避免包装的签名 URL 泄露。认证 API 客户端禁止跨 origin 重定向，因为 SDK 的 auth transport 会对重定向重新附加 token；附件继续使用不含 token 的客户端。Token 校验拒绝空白与控制字符，不回显值。
