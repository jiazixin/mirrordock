# MirrorDock · 源仓

Go **1.27.2** + Vue 3 的软件源同步与 HTTP/HTTPS 镜像服务。生产目标为 Linux，macOS 用于开发和本机验证；当前只支持这两个平台。**同步要求镜像所在文件系统实测支持 reflink（如开启 reflink 的 XFS、APFS）；不支持的 ext4/NFS 等环境不能同步，且不回退完整复制。**支持系统 rsync、可选 DNF 4 reposync、GitHub Release assets 下载；管理台位于 `/admin/`，管理 API 位于 `/api/`，公开镜像直接使用 `/<dest>/`。

支持手动和 cron 同步、备用来源、累计超时、自动重试、暂停/继续/取消、SQLite 历史、日志详情与手动清理、运行分析及任务结果通知。同步成功并校验后发布，失败保留上一发布代。当前验收结果与部署环境待验项见 [ACCEPTANCE.md](docs/ACCEPTANCE.md)。

## 安装与构建

需要 Go 1.27.2、Node.js >=22.12、npm。部署 rsync 应为包含发行版安全补丁的 **3.1+** 版本；macOS 自带旧版本不支持所需参数。Reposync 使用 DNF 4、`dnf-plugins-core` 和开启 GPG 检查时所需的 `rpmkeys`；受控仓库已验证 Rocky 9 和 AlmaLinux 10.2，具体架构与范围见验收记录，不承诺兼容 DNF5。GitHub 下载不需要 gh。

从仓库根执行，必须先生成 Web，再编译 Go：

```sh
npm --prefix web ci
sh scripts/build.sh
./bin/mirrordock --version
```

发布 tag 采用 `v主版本.次版本.修订版本`，例如 `v1.1.0`、`v1.2.0`，预发布为 `v1.2.0-rc.1`；不使用 `1.1`、`v1.2` 等非规范 tag。普通构建不需要注入版本：Go 1.24 起会在可识别的 Git 仓库中将模块版本写入二进制，本项目通过 `debug.ReadBuildInfo().Main.Version` 读取。精确标准 tag 显示对应版本，非标签提交显示 Go 生成的伪版本，未提交修改保留 `+dirty`；额外显示 12 位提交 ID。应以包路径构建，并保留 `.git` 与对应 tag，不使用 `-buildvcs=false` 关闭此能力。Go 模块的 tag 必须匹配模块路径的主版本及子目录规则，见 [Go 官方版本说明](https://go.dev/ref/mod#versions)。

版本优先级为显式链接注入（非空且非 `dev`）→ Go 主模块版本（非空且非 `(devel)`）→ 12 位提交 ID → `dev`。显式覆盖仍保留可取得的 modified 状态；运行时不调用 Git。管理台“系统配置”和 `/api/system/info` 显示与 CLI 相同的实际构建版本。`--version` 和 `--help` 不读取配置或启动服务。直接构建与交叉编译如下：

```sh
npm --prefix web run build
go build -trimpath -o bin/mirrordock ./cmd/mirrordock
# 或由脚本先构建 Web，再交叉编译 Go
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 sh scripts/build.sh
```

源码压缩包没有 Git 信息时可显式注入；以下 `v1.1.0` 是示例，必须替换为与源码对应的真实版本，不能把未知来源或未提交修改标为正式发行版：

```sh
npm --prefix web run build
go build -trimpath -ldflags='-X main.version=v1.1.0' -o bin/mirrordock ./cmd/mirrordock
```

`web/dist/` 是唯一 Web 输出，由 `web/embed.go` 嵌入二进制，不提交 Git。干净检出直接执行 Go 构建或全量测试会因缺少嵌入文件失败。容器构建也先构建 Web。Docker 上下文不包含 Git，需传入 VERSION；未传时显示 dev，容器构建不附带 Git dirty 元数据。管理页面按路由加载，资源随二进制发布，无 CDN 或远程字体。

Logo 唯一源为 `web/src/assets/logo.svg`。修改后用安装了 Pillow 的 Python 执行 `python3 scripts/generate-favicon.py`，再构建；普通构建不需要 Python。公开目录 Phosphor Regular 图标随 `internal/mirrors/listing-icons.html` 嵌入，包含 MIT 授权；`scripts/import-directory-icons.py` 仅用于从固定上游提交重新导入。

## 首次启动

所有字段与源案例见 [configs/config.example.yaml](configs/config.example.yaml)。本机试用可先用空源配置：

```sh
cat > configs/config.local.yaml <<'YAML'
listen: 127.0.0.1
port: 8080
path: ../data/state
mirror_root: ../data/mirrors
auth:
  username: admin
  password: 请替换为你的密码
sources: []
YAML
./bin/mirrordock --config configs/config.local.yaml
```

替换密码后访问 `http://127.0.0.1:8080/admin/`。账号来自 YAML，密码启动时生成内存 bcrypt 校验值；无初始化码、数据库管理员密码或设置密码 API。用户名必须非空、最多 128 字节，无首尾空白或控制字符；密码必须非空且最多 72 字节。修改账号后重启，原会话失效。配置读取 API 不返回管理员密码。

页面新增源后手动执行一次同步。新增 GitHub Release 源时可开启“批量添加”，一次填入多个仓库，在预览表中逐行调整 id、名称、dest 与附件 glob，Cron 分钟自动错开，整批一次写回 YAML。案例的上游和自动计划必须逐项确认；不要直接运行完整案例配置进行测试。程序按 `type` 选择执行器，不依据名称推断工具。每种类型只应用自身选项；切换类型保留的其他类型草稿不参与执行，不能据此推断那些参数已生效。

## 配置协议

`-f <文件>` 与 `--config <文件>` 等价，同时指定不同文件报错。无参数读取工作目录 `config.yaml`，不存在就报错，不生成或覆盖。相对路径以配置文件目录为基准，数据库为 `<path>/data.sqlite`，任务日志为 `<path>/logs/<run-id>/<attempt>.log`。启动自动建表/索引并保留原有记录。

用户确认的 YAML 协议优先于 [目录规范.md](目录规范.md) 的默认协议。不使用 CONFIG_FILE 或环境变量覆盖配置，不恢复 version、timezone、maxconcurrent、exec、token_env、records 字段；按类型并发使用用户确认的 `settings.concurrency`。系统启动时区用于调度、管理台和公开目录；可在启动环境设 `TZ=Asia/Shanghai`。通知时区单独由登录浏览器同步，未同步时 UTC。

- `listen`、`port`、`path`、`mirror_root`、TLS、`auth`、`log` 修改后重启。系统配置页仅显示路径和环境检查；页面保存其他配置时保留这些部署字段。
- YAML 是配置真源；保存保留注释、检查 revision，使用同目录临时文件、fsync 与 rename。配置文件及父目录必须允许程序身份写入，应挂载整个配置目录。外部编辑后用“重新载入配置”，冲突时重新打开编辑器。设置页保留未保存草稿：通知/源更新可合并，实际设置冲突时须显式载入最新设置后重新编辑。
- `settings.concurrency` 按类型限制同时执行的任务数（rsync、reposync、github_release 各自 1–16，默认 4），各类型独立计数、互不占用名额；超出的任务按触发顺序排队，系统设置“同步策略”页保存后立即生效，已在执行的任务不受影响。同源已有排队、运行、暂停或中断任务时跳过重复触发。cron 使用五字段，`overlap` 仅支持 `skip`。
- 服务停止/重启产生的 `SERVICE_INTERRUPTED` 在源 ID、类型、dest 匹配且仍启用时自动继续，沿用剩余预算和重试次数；预算耗尽转超时。暂停与持久化失败等其他中断不自动继续。启动按任务 ID 分页恢复全部未终态记录；停机期间源已从 YAML 删除或变更类型/dest 的任务自动取消（`SOURCE_REMOVED`/`SOURCE_CHANGED`），已禁用源的排队任务转为中断（`SOURCE_DISABLED`），启用后可继续。worker 出队与“继续”操作同样核对当前配置，不按旧快照执行。
- `task_timeout` 默认 8h，准备、传输、校验、发布、来源切换与重试等待共享累计预算；排队/暂停不计时。停止时先 TERM，最多 10 秒后 KILL 整个进程组；执行器正常退出后也清理同组残留子进程。服务被 SIGKILL/OOM 终止时 rsync 派生的接收进程会脱离父进程继续写入，下次启动在恢复任务前结束本用户所有参数引用 `<mirror_root>/.mirrordock/work/` 的进程（只看参数、不看工作目录，不碰发布代和其他用户的进程）。systemd 的 `KillMode=control-group` 也会在重启前清理整个 cgroup，但残留进程忽略 TERM 时要等满 `TimeoutStopSec`。
- `retry_count` 默认额外 3 次，范围 0–10，0 关闭。网络/临时上游错误按 5/10/20 秒退避、后续最多 80 秒；次数和等待期限持久化，继续不会重置。单个 rsync 来源的认证、模块不存在等错误仍可切换下一个来源；本地权限/磁盘错误停止。rsync 23/24 不作为成功发布，仓库校验不一致进入有限重试/备用来源流程。GitHub 配额限流按服务端重置时间延迟重试，等待仍占累计预算。
- 数据、配置和 TLS 文件必须与镜像根隔离。`dest` 必须是规范相对目录，禁止保留路径、嵌套、重复，以及解析符号链接后重叠或越界的目录。

### 执行器

Rsync 使用 `src1`、`src2` 等按数字升序尝试；各来源应为同一仓库布局。地址必须包含模块；模块根可为 `rsync://host/module`，模块内目录须以 `/` 结尾。每轮耗尽备用来源后才自动重试；本轮任一来源为网络/临时错误即重试整轮，所有来源都是认证、模块不存在等永久错误时直接失败。`exclude` 仅阻止同步，不删除以前已发布的匹配文件；未开启 `--delete-excluded`。`delete`、保留链接/时间/权限/硬链接默认 true；不默认保留上游属主或设备文件。`limit_mb` 是单个上游任务的十进制 MB/s，0 不限速，不限制公开下载。UID/GID 可分别填写，0 有效，范围 0–4294967294；跨身份映射依赖服务用户权限，程序不提权。

Reposync 使用单个 `src1`，生成任务私有 repo、缓存和密钥库，不修改 `/etc/yum.repos.d`。`download_metadata=false` 为仅包文件模式，可发布下载文件，但不能直接用作 DNF 仓库，并会去除上次遗留的 repodata。完整镜像要求下载元数据，并拒绝 `download_metadata=true` 与 `newest_only=true` 组合。GPG 包签名检查不等于仓库元数据签名验证；参数和真实发行版安装仍须在目标 EL 验收。

配置中的来源地址和 `gpg_keys` 为公开地址，不支持 URL 鉴权，不能包含用户信息、查询参数或片段；这些地址会作为运行事实保存。GitHub 从 API 获取的临时签名资产 URL 不受此配置限制。运行快照仍防御性脱敏。数据库结构版本记录在 SQLite `PRAGMA user_version`（1.0 为 1）；程序拒绝打开版本更高的数据库。以后改变表结构必须提升版本并附带显式迁移步骤。

GitHub 按发布时间选最近 N 个符合条件的 Release，仅下载 assets，不下载自动源码 ZIP/TAR。源内 `release_count: 0` 继承系统值；glob 可筛选附件。tag 和附件不得使用 `.mirrordock*`、`.rsync-partial`、`.~tmp~` 或 `*.part` 等内部保留名称。大小、身份及可用 digest 校验后才复用或续传，Range 不兼容时重新下载。正文读取连续 5 分钟无数据会中止并保留 `.part`，允许有限重试/续传。开启旧版本清理后，仅在所有选中附件下载成功后清除旧版本及保留版本内已删除、改名或不再匹配的附件。为保证发布时间选择及附件完整性，仍分页查询 Release 和附件，超过 100 页明确失败。

GitHub Token 直接存放 `settings.github.token`，读取 API 仅返回存在标记，页面留空保留，显式清空删除。`settings.github.proxy` 支持 HTTP/HTTPS/SOCKS5/SOCKS5h，API 和附件共用，空字符串直连，不读取代理环境变量；代理失败不绕过代理。HTTP/mixed 入口用 `http://`，`https://` 要求代理入口自身支持 TLS；特殊字符需 URL 编码。**按已确认协议，受登录保护的 API 返回完整代理地址，页面明文回填；其认证信息仍不进入运行快照、审计或日志。** Token 与代理在每次实际执行时读取。上述直连规则仅针对 GitHub；DNF、rsync 子进程继承启动环境，GPG 公钥 HTTP 下载使用环境代理。

### 日志与 TLS

顶层 `log`：level 为 debug/info/warn/error，默认 info；format 为 json/text，默认 json；path 为空输出标准输出，非空为相对配置目录的追加文件。程序不自动轮转；可停机归档或使用 logrotate copytruncate。日志不能位于镜像根，不能覆盖配置、数据库、锁、通知密钥或 TLS 文件；任务日志子目录保留给原始输出。页面清理仅处理成功/失败/超时/取消四种终态任务日志，保留记录和错误摘要；可恢复的 interrupted 不清理。删除后保存失败也返回实际删除数量。

手动清理以开始时的任务编号为上界，每页最多 100 条；文件删除和保存不占用整个运行引擎锁。相同任务的继续/取消等待该任务清理结束，其他任务的控制、进度与检查点继续执行。请求取消或服务退出停止后续清理，已保存的完成标记保留，失败可重试；已打开的日志下载仍读取打开时的完整长度。外部备份中的日志不受页面清理影响。

TLS cert/key 同时留空提供 HTTP，填齐且匹配提供同端口 HTTPS，改动后重启。Go TLS 支持 HTTP/2，HTTPS 不宣称 sendfile。生产管理会话需要保密时启用 TLS。当前不信任 `X-Forwarded-*`，不支持“代理终止 HTTPS、后端使用 HTTP”的管理部署；可采用 TLS 透传或代理到启用 TLS 的后端并保留外部 Host/Origin。登录按实际连接 IP（IPv6 按 /64 前缀）每分钟最多 10 次，成功后重置，最多 4096 个限流桶，满额时淘汰最早的窗口而不是拒绝新客户端；同一反向代理后的客户端仍共用代理 IP，需在代理侧补充限流。

## 管理界面与统计

管理列表默认每页 50，可选 20/50/100，支持首尾页和输入跳页；运行与通知查询覆盖完整历史。运行列表的计数与取页共用只读事务，最多四条只读连接与写连接隔离；管理运行查询使用请求取消和 5 秒数据库查询期限。源状态用一次 SQL 按每源索引读取最新任务，不受其他源历史数量影响；SSE 客户端共用最多一秒的序列化快照，登录后的写操作会使其立即失效并推送新快照；连接期间运行状态只取自 SSE，不与刷新结果混用，避免状态回跳。批次与记录支持最近 1/12/24 小时、昨天、3/7/30 天和本月；自然日/月使用程序启动时区。

批次“实际接收”累计各次尝试：Rsync 为 `Total bytes received`（包含协议数据，命令结束后更新），GitHub 为实际读取的资产响应正文（不含复用、已有续传片段、HTTP/TLS 头部）。旧记录、Reposync 或未报告的尝试显示未统计/部分统计。运行记录“数据量”为执行器处理量，不等于网络流量。

Rsync / Reposync / Release 分析按任务创建日期聚合完整历史，支持 7/30/90 天和同步源。成功率排除取消及未结束任务；耗时不含排队/暂停。处理量只累计成功任务最终快照，包含复用/增量语义。Reposync 不展示无法可靠取得的包数/处理量。只读连接与 5 秒超时限制查询，密集数据可能需要重试。

详情按同步阶段、统计、来源尝试和折叠区组织。活动及可恢复任务每 3 秒更新状态；成功、失败、超时、取消后停止状态轮询，可手动刷新。原始日志默认收起，展开才读取；收起或页面切到后台即取消读取，返回后重新校对。终态日志按每轮最多 2 MiB 继续读取到末尾，页面只保留最后 2 MiB 文本；历史尝试选择与滚动跟随设置保留，下载获取点击时该尝试已有的完整日志。关闭详情停止请求与计时器。反馈提示 5 秒关闭、最多三条；表单错误保留在相关内容中。窄屏时顶部导航收为下拉菜单，选项卡单列、表格横向滚动，选择框和弹窗可用键盘操作。管理台与公开目录只提供浅色界面。登录后的首页为概览：同步时间轴按源显示过去 24 小时任务（最多读取最近 500 个）和未来 12 小时的下一次计划，并列出运行中与需要处理的任务；页面在后台时暂停刷新。

## 通知与备份

五个渠道：企业微信、钉钉、Telegram、Bark、通用 Webhook，默认关闭，分别保存和测试。先保存再测试，测试使用已保存凭据，不依赖自动开关。企业微信/钉钉限定官方机器人地址；Bark 输入服务地址/设备 Key；Webhook 接收 POST JSON `{event,title,body}`，事件为 `batch.finished` 或 `notification.test`。请求超时 10 秒、不重定向、响应上限 64 KiB，错误不返回远端原始正文。通知客户端目前直连，不读取系统/GitHub 代理配置；通用 Webhook 尚无 HMAC 或自定义鉴权头。需在实际部署网络确认可达性，Webhook 接收端应使用 HTTPS、不可猜测的 endpoint 和网络访问控制；这不等价于消息签名。

启用时记录最大任务编号，仅通知后续新建任务；每渠道每任务一条记录。仅失败包括失败、超时及含失败子项的终态；interrupted 不占用最终结果或触发失败通知，继续后的真正终态才通知。Rsync 通知分别显示条目总数和本次传输普通文件数，不把未变化文件误称为“未完成”。独立 worker 每 3 秒扫描，最多尝试 3 次，失败等待 30/60 秒；通知失败不改变同步结果。远端已收到而响应丢失时可能重复送达，以正文任务编号识别。记录页每 5 秒刷新，离开即停止。

通知 endpoint/token/secret 在 YAML 中为绑定渠道/字段的 AES-256-GCM `enc:v1:` 密文，密钥为 `<path>/notification.key`、0600 普通文件。读取 API 只返回存在标记；敏感输入留空保留，显式清空才删除。不要在 YAML 填通知明文凭据；已有密文时密钥丢失/不匹配会阻止启动。

备份应先正常停止服务，再一起备份 YAML、**完整 state 目录**（SQLite、通知密钥、队列、时区等）和镜像根。只备份 YAML 或 SQLite 无法恢复通知凭据。在线备份需保证 SQLite 一致性，不能只复制写入中的数据库。忘记密码时修改 YAML 后重启。

## 下载与发布

服务仅公开配置中的发布源，不暴露整个磁盘根。工作目录为 `.mirrordock/work/<run-id>`，成功代位于 `.mirrordock/published/<uuid>`；发布指针先持久化再切换路由。旧代至少保留 24 小时，有活动引用不回收，缺失旧包可回退读取。启动与配置保存/重载均可校验并导入已有 dest 目录，重新添加保留的源或更换 ID 使用同一 dest 会恢复发布路由；修改 dest 复用原发布代并移除已确认归属的旧链接。单个源接入失败（如 dest 被普通文件占用、已有目录校验不通过）只影响该源：启动与其他源照常，错误写入程序日志并显示在环境检查“发布目录接入”。配置已写入而目录接入失败时，保存/重载返回新 revision 并提示警告，修复后重新载入配置。预先创建的空 dest 目录视为占位，首次发布时替换为发布链接；非空目录不会被删除。同一源 ID 改变类型后继续公开旧内容，下次同步从空工作树开始，不克隆其他类型的布局。部署后不要直接修改发布代。删除源撤销路由/计划，不删除镜像文件；活动、暂停、中断任务必须先取消才能改类型或 dest。

已删除源的发布数据默认长期保留，不提供一键删除接口。如需回收：先取消该源可恢复任务、从 YAML 删除源，正常停机并备份完整 state 与镜像根；使用 SQLite 管理工具按精确 `source_id` 检查 `publications` 行，核对其 current/previous 代是否被其他行复用，在事务中仅删除确认不再需要的该源发布记录，保留 runs/通知等事实。旧 dest 仅在确认为该源的符号链接、且未被其他源接管时解除；不要递归删除 dest 或直接删除 published 代。启动后由原有 GC 按引用与 24 小时规则回收不再引用的代；复用代会继续保留。重新添加已删除记录的源前，需先处理残留别名或恢复备份。操作有误应停机还原整套备份，本轮未操作用户的实际数据库或镜像。

发布路由保存代和引用。镜像锚点固定打开，受限代根使用最多 32 项的 LRU 缓存；包括锚点在内不超过 FD 预留额度的四分之一，软限制 128 时最多缓存 15 项。请求在打开文件前取得 FD 配额，只在取得真实文件期间借用根，之后保留文件与代引用；缓存满且根都被借用时使用瞬态根，回收和退出释放缓存。冷路径的文件系统查找不持有缓存锁，不阻塞已缓存根的借用。TCP 与文件请求按同一进程软限制分配额度，保留数据库/同步/日志余量。文件配额同时约束 GET、HEAD、目录和 HTTP/2 多流，饱和返回 503/Retry-After。

同步要求镜像目录实测支持 reflink：Linux 使用 FICLONE（生产 XFS 必须开启 reflink），macOS 开发环境使用 APFS 原生克隆；published 与 work 必须位于允许克隆的同一文件系统。克隆失败停止任务，不回退完整复制。多代中未变化的文件共享数据块，新增/替换的文件及被旧代保留的内容仍实际占用空间；首次同步仍需整套镜像容量。只有工作树元数据与修改身份均未变化才复用当前代，不生成新代；这一检查不读取文件正文。Rsync 保留单文件临时替换与 partial-dir，保护内部标记，不使用 delay-updates 或 inplace。

安全余量为文件系统总量的 1% 与 1 GiB 取大，任务启动另为每个活动 worker（含新任务）预留 512 MiB。空间不足时保持排队且不消耗运行预算，恢复后自动执行；运行中每 5 秒检查，低于安全余量停止任务并保留上一发布；已进入发布阶段的任务不再停止，因为发布只是同一文件系统内的改名、不需要新空间。此机制不是硬配额，检查间隔内的突增或外部写入仍可能触发 ENOSPC。容量探测异常会记录错误并让新任务保持排队（不消耗预算、恢复后自动执行），不等同于磁盘不足而取消正在运行的任务。DNF 私有数据放在任务 work 的隐藏子目录；启动时清理上次未完成的 reflink 探测目录和待删除工作目录（`.discard-*`）。持久化的成功任务清理 work；失败/超时/取消任务的 work 若已完成准备，则作为该源下次同步的起点保留 7 天（每源只留最新一份，磁盘不足导致的失败、源被删除或改类型/dest 时不保留），下次任务直接改名接手、不复制，rsync 跳过已完成文件并从 `.rsync-partial` 续传；有任务因空间不足排队时，按结束时间从旧到新回收其他源的保留目录，直到可以放行，排队源自己的保留目录不回收。暂停/中断及未知目录保留；服务启动、终态及每 5 分钟触发回收，过期旧代仍遵守引用保护。系统设置的环境检查显示 reflink 支持/不支持/探测失败及原因，并显示实际文件系统容量、余量和目录数量；刷新会重新实测，不将各代逻辑大小相加当作物理占用。

公开目录可搜索/排序，不分页，最多列出文件系统遍历得到的前 1000 个可见项并提示截断；搜索/排序只作用于已列出项，不代表全目录分页；隐藏项不占名额。其他文件仍可用完整路径下载。无 JavaScript 时仍有已列出的链接。项目行展示状态、公开文件逻辑大小和上次/下次同步；状态读取使用最多两次批量索引查询及一秒共享缓存，绑定请求取消，正文下载仍不访问数据库；GitHub 版本行展示大小。符号链接不回显目标，私有标记不可下载。

文件下载保留真实文件流、HTTP/1.1 sendfile 调用链、Range/HEAD/条件请求；热路径不访问 SQLite、不哈希全文件、不逐块记录日志、不动态压缩。有界资源和生产验收边界见 [DESIGN.md 的 ADR-010](docs/DESIGN.md#adr-010下载性能与资源隔离) 与 [ACCEPTANCE.md](docs/ACCEPTANCE.md)。

## 开发验证

```sh
npm --prefix web run format:check
npm --prefix web run build
go vet ./...
go test -race -count=1 ./...
go test ./internal/mirrors -run '^$' -bench BenchmarkPublicHTTP -benchmem
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/mirrordock-linux-amd64 ./cmd/mirrordock
```

格式化使用 `npm --prefix web run format`。真实 rsync 和 E2E 用隔离 daemon/临时目录，显式选择现代 rsync；程序启动测试时也需将相应目录放在 PATH：

```sh
(cd web && npm exec -- playwright install chromium)
PATH=/path/to/modern-rsync:$PATH MIRRORDOCK_TEST_RSYNC=/path/to/modern-rsync/rsync go test -race -count=1 ./...
# 先构建 Web 和测试二进制；变量仅用于测试，不属于产品配置
PATH=/path/to/modern-rsync:$PATH MIRRORDOCK_TEST_RSYNC=/path/to/modern-rsync/rsync \
  MIRRORDOCK_TEST_BINARY=/absolute/temp/mirrordock npm --prefix web run test:e2e
```

未设置测试 rsync 时部分协议测试会跳过；测试通过不代表跳过项已验证。报告位于根 `test-results/` 和 `web/test-results/`，均忽略。项目未连接外部 CI，不宣称运行远程流水线。

### 依赖来源校验

依赖版本由 `go.sum` 与 `web/package-lock.json` 固定。上线或升级依赖前从源头核验，不依赖第三方代理的缓存：

```sh
# 用全新模块缓存直接从各模块源仓库拉取，并对照官方校验数据库 sum.golang.org
GOMODCACHE=$(mktemp -d) GOPROXY=direct GOSUMDB=sum.golang.org go mod download all
go mod verify
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
# npm：锁文件 integrity 来自 registry.npmjs.org，并校验注册表签名与来源证明
npm --prefix web ci --ignore-scripts
npm --prefix web audit signatures
npm --prefix web audit
```

`go mod download all` 在全新缓存下可能向 `go.sum` 追加未参与构建的模块校验行，核验后用 `git checkout go.sum` 还原。Dockerfile 的三个基础镜像按 digest 固定，更新时先用 `docker buildx imagetools inspect <镜像:tag>` 取得官方摘要并评审变更。

## 部署与目录

`docker build --build-arg VERSION=v1.1.0 -t mirrordock .` 使用先 Vue 后 Go 的多阶段构建；VERSION 示例必须替换为与源码对应的真实版本，构建上下文无 `.git`，无法自动核对版本或 dirty 状态。三个基础镜像（Node、Go、Rocky Linux 官方发布的 `rockylinux/rockylinux:9`）均按 digest 固定；Web 依赖以 `npm ci --ignore-scripts` 安装，Go 模块下载后执行 `go mod verify`。运行层先应用已发布安全更新，再安装 rsync/DNF。Docker 与真实 EL 验收以当前验收文档为准。配置中的 path 和 mirror_root 可设为 `/var/lib/mirrordock` 与 `/srv/mirrors`。

国内网络可使用 `docker build --build-arg VERSION=v1.1.0 --build-arg GOPROXY=https://goproxy.cn --build-arg ROCKY_MIRROR=https://mirror.nju.edu.cn/rocky -t mirrordock .`（同样替换示例版本）。参数选择构建依赖下载源，默认仍为 Go 官方代理和 Rocky 镜像列表，保留 Go 校验数据库（`GOSUMDB=sum.golang.org`）与 RPM 签名检查；`ROCKY_MIRROR` 必须是 `https://` 地址，只允许 URL 安全字符；Rocky 源保留在镜像的软件源配置中，应用同步仍使用任务私有仓库。基础镜像加速由 Docker 引擎的 `registry-mirrors` 设置，例如按 [毫秒镜像说明](https://1ms.run/guide) 配置用户选择的源；这些设置不写入应用 YAML。

```sh
docker run --rm --name mirrordock -p 8080:8080 -e TZ=Asia/Shanghai \
  -v /absolute/config-dir:/etc/mirrordock \
  -v /absolute/state-dir:/var/lib/mirrordock \
  -v /absolute/mirror-dir:/srv/mirrors mirrordock
```

镜像默认以非 root 用户 `mirrordock`（UID/GID 10001，可用 `--build-arg MIRRORDOCK_UID=` 调整）运行；挂载的配置目录、state、镜像卷须预先授予该身份读写权限，例如 `chown -R 10001:10001 <目录>`。也可用 `--user UID:GID` 指定其他身份。映射其他 UID/GID 或绑定低端口需要额外权限。未内置固定 HEALTHCHECK，因为端口、HTTP/TLS 由 YAML 决定；部署方应针对实际入口配置健康探测。

systemd 模板见 [deploy/mirrordock.service](deploy/mirrordock.service)，部署前创建服务用户、目录并赋权，按实际位置调整路径。低端口另需绑定权限。

目录基线只维护于 [目录规范.md](目录规范.md)，协作规则见 [AGENTS.md](AGENTS.md)，当前实现见 [IMPLEMENTATION.md](docs/IMPLEMENTATION.md)。

```text
cmd/mirrordock/           CLI 入口
internal/{config,observability,database,sync,runs,mirrors,notifications,app}/
web/                     唯一 npm/Vite 边界；Vue、E2E、embed.go
configs/                 注释案例；本地实例忽略
deploy/                  systemd 定义
scripts/                 构建、favicon 与目录图标生成工具
docs/                    当前设计、实施与验收
bin/ data/ .cache/        忽略的本机二进制、状态与缓存
test-results/            忽略的 Go/OCR/构建报告
```

| 基线规则 | 实际行为 | 原因与依据 | 适用范围 / 重新评估条件 |
| --- | --- | --- | --- |
| §7.1 配置入口与覆盖 | -f/--config；默认 cwd/config.yaml；无字段环境覆盖 | 用户确认的 YAML 协议 | 本程序；仅用户要求改变时评估 |
| §7.1 密钥注入 | GitHub Token 直接 YAML；通知凭据 YAML 密文；代理 URL 管理端回填 | 已确认配置与代理编辑协议 | 配置 API/任务；凭据协议改变时评估 |
| §11 工具必需路径 | web/embed.go 嵌入 web/dist | Go embed 与 Vite 的实际构建边界 | Web 资源；交付方式改变时评估 |
| §7.2 日志位置 | log.path 可为部署绝对路径或相对配置目录 | 已确认顶层 log 字段；验证与镜像/敏感路径隔离 | 程序日志；日志协议改变时评估 |
