# MirrorDock · 源仓

Go **1.27.1** + Vue 3 的软件源同步与 HTTP/HTTPS 镜像服务。生产目标为 Linux，macOS 用于开发和本机验证。支持系统 rsync、可选 DNF 4 reposync、GitHub Release assets 下载；管理台位于 `/admin/`，管理 API 位于 `/api/`，公开镜像直接使用 `/<dest>/`。

支持手动和 cron 同步、备用来源、累计超时、自动重试、暂停/继续/取消、SQLite 历史、日志详情与手动清理、运行分析及任务结果通知。同步成功并校验后发布，失败保留上一发布代。当前验收结果与部署环境待验项见 [ACCEPTANCE.md](docs/ACCEPTANCE.md)。

## 安装与构建

需要 Go 1.27.1、Node.js >=22.12、npm。部署 rsync 应为包含发行版安全补丁的 **3.1+** 版本；macOS 自带旧版本不支持所需参数。Reposync 针对 EL8/EL9 的 DNF 4、`dnf-plugins-core` 和开启 GPG 检查时所需的 `rpmkeys`，不承诺兼容 DNF5。GitHub 下载不需要 gh。

从仓库根执行，必须先生成 Web，再编译 Go：

```sh
npm --prefix web ci
npm --prefix web run build
mkdir -p bin
go build -trimpath -o bin/mirrordock ./cmd/mirrordock
```

`web/dist/` 是唯一 Web 输出，由 `web/embed.go` 嵌入二进制，不提交 Git。干净检出直接执行 Go 构建或全量测试会因缺少嵌入文件失败。容器构建也先构建 Web。管理页面按路由加载，资源随二进制发布，无 CDN 或远程字体。

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

页面新增源后手动执行一次同步。案例的上游和自动计划必须逐项确认；不要直接运行完整案例配置进行测试。程序按 `type` 选择执行器，不依据名称推断工具。

## 配置协议

`-f <文件>` 与 `--config <文件>` 等价，同时指定不同文件报错。无参数读取工作目录 `config.yaml`，不存在就报错，不生成或覆盖。相对路径以配置文件目录为基准，数据库为 `<path>/data.sqlite`，任务日志为 `<path>/logs/<run-id>/<attempt>.log`。启动自动建表/索引并保留原有记录。

用户确认的 YAML 协议优先于 [目录规范.md](目录规范.md) 的默认协议。不使用 CONFIG_FILE 或环境变量覆盖配置，不恢复 version、timezone、maxconcurrent、exec、token_env、records 字段。系统启动时区用于调度、管理台和公开目录；可在启动环境设 `TZ=Asia/Shanghai`。通知时区单独由登录浏览器同步，未同步时 UTC。

- `listen`、`port`、`path`、`mirror_root`、TLS、`auth`、`log` 修改后重启。系统配置页仅显示路径和环境检查；页面保存其他配置时保留这些部署字段。
- YAML 是配置真源；保存保留注释、检查 revision，使用同目录临时文件、fsync 与 rename。配置文件及父目录必须允许程序身份写入，应挂载整个配置目录。外部编辑后用“重新载入配置”，冲突时重新打开编辑器。
- 内置两个同步 worker，同源已有排队、运行、暂停或中断任务时跳过重复触发。cron 使用五字段，`overlap` 仅支持 `skip`。
- `task_timeout` 默认 8h，准备、传输、校验、发布、来源切换与重试等待共享累计预算；排队/暂停不计时。停止时先 TERM，最多 10 秒后 KILL 整个进程组。
- `retry_count` 默认额外 3 次，范围 0–10，0 关闭。网络/临时上游错误按 5/10/20 秒退避、后续最多 80 秒；次数和等待期限持久化，继续不会重置。认证、证书、权限、磁盘和配置错误停止。
- 数据、配置和 TLS 文件必须与镜像根隔离。`dest` 必须是规范相对目录，禁止保留路径、嵌套、重复，以及解析符号链接后重叠或越界的目录。

### 执行器

Rsync 使用 `src1`、`src2` 等按数字升序尝试；各来源应为同一仓库布局。每轮耗尽备用来源后才自动重试。`delete`、保留链接/时间/权限/硬链接默认 true；不默认保留上游属主或设备文件。`limit_mb` 是单个上游任务的十进制 MB/s，0 不限速，不限制公开下载。UID/GID 可分别填写，0 有效，范围 0–4294967294；跨身份映射依赖服务用户权限，程序不提权。

Reposync 使用单个 `src1`，生成任务私有 repo、缓存和密钥库，不修改 `/etc/yum.repos.d`。完整镜像要求下载元数据，并拒绝 `download_metadata=true` 与 `newest_only=true` 组合。GPG 包签名检查不等于仓库元数据签名验证；参数和真实发行版安装仍须在目标 EL 验收。

配置中的来源地址和 `gpg_keys` 为公开地址，不支持 URL 鉴权，不能包含用户信息、查询参数或片段；这些地址会作为运行事实保存。GitHub 从 API 获取的临时签名资产 URL 不受此配置限制。启动时按 100 条分页脱敏历史 `runs` 与 `run_results` 中的来源/公钥地址及相关尝试、事件和错误 URL，成功后记录迁移标记；失败阻止启动并可重试，不删除记录。

GitHub 按发布时间选最近 N 个符合条件的 Release，仅下载 assets，不下载自动源码 ZIP/TAR。源内 `release_count: 0` 继承系统值；glob 可筛选附件。大小、身份及可用 digest 校验后才复用或续传，Range 不兼容时重新下载。开启旧版本清理也只在本轮成功后生效。

GitHub Token 直接存放 `settings.github.token`，读取 API 仅返回存在标记，页面留空保留，显式清空删除。`settings.github.proxy` 支持 HTTP/HTTPS/SOCKS5/SOCKS5h，API 和附件共用，空字符串直连，不读取代理环境变量；代理失败不绕过代理。HTTP/mixed 入口用 `http://`，`https://` 要求代理入口自身支持 TLS；特殊字符需 URL 编码。**按已确认协议，受登录保护的 API 返回完整代理地址，页面明文回填；其认证信息仍不进入运行快照、审计或日志。** Token 与代理在每次实际执行时读取。

### 日志与 TLS

顶层 `log`：level 为 debug/info/warn/error，默认 info；format 为 json/text，默认 json；path 为空输出标准输出，非空为相对配置目录的追加文件。程序不自动轮转；可停机归档或使用 logrotate copytruncate。日志不能位于镜像根，不能覆盖配置、数据库、锁、通知密钥或 TLS 文件；任务日志子目录保留给原始输出。页面清理仅处理终态任务日志，保留记录和错误摘要。

TLS cert/key 同时留空提供 HTTP，填齐且匹配提供同端口 HTTPS，改动后重启。Go TLS 支持 HTTP/2，HTTPS 不宣称 sendfile。生产管理会话需要保密时启用 TLS。出现 `field auth not found` 表示部署了旧二进制，应重新构建、替换实际程序并重启。

## 管理界面与统计

管理列表默认每页 50，可选 20/50/100，支持首尾页和输入跳页；运行与通知查询覆盖完整历史。源状态按每源最新任务读取，不受其他源历史数量影响。批次与记录支持最近 1/12/24 小时、昨天、3/7/30 天和本月；自然日/月使用程序启动时区。

批次“实际接收”累计各次尝试：Rsync 为 `Total bytes received`（包含协议数据，命令结束后更新），GitHub 为实际读取的资产响应正文（不含复用、已有续传片段、HTTP/TLS 头部）。旧记录、Reposync 或未报告的尝试显示未统计/部分统计。运行记录“数据量”为执行器处理量，不等于网络流量。

Rsync / Reposync / Release 分析按任务创建日期聚合完整历史，支持 7/30/90 天和同步源。成功率排除取消及未结束任务；耗时不含排队/暂停。处理量只累计成功任务最终快照，包含复用/增量语义。Reposync 不展示无法可靠取得的包数/处理量。只读连接与 5 秒超时限制查询，密集数据可能需要重试。

详情每 3 秒更新状态、尝试与日志；历史尝试选择不会被新尝试覆盖。日志最多保留当前 2 MiB 文本，可暂停滚动跟随；下载获取点击时该尝试已有的完整日志。关闭页面会停止轮询。反馈提示 5 秒关闭、最多三条；表单错误保留在相关内容中。窄屏支持侧栏、单列选项卡和横向表格滚动，选择框和弹窗可用键盘操作。

## 通知与备份

五个渠道：企业微信、钉钉、Telegram、Bark、通用 Webhook，默认关闭，分别保存和测试。先保存再测试，测试使用已保存凭据，不依赖自动开关。企业微信/钉钉限定官方机器人地址；Bark 输入服务地址/设备 Key；Webhook 接收 POST JSON `{event,title,body}`，事件为 `batch.finished` 或 `notification.test`。请求超时 10 秒、不重定向、响应上限 64 KiB，错误不返回远端原始正文。

启用时记录最大任务编号，仅通知后续新建任务；每渠道每任务一条记录。仅失败包括失败、超时、中断及含失败子项的终态。独立 worker 每 3 秒扫描，最多尝试 3 次，失败等待 30/60 秒；通知失败不改变同步结果。远端已收到而响应丢失时可能重复送达，以正文任务编号识别。记录页每 5 秒刷新，离开即停止。

通知 endpoint/token/secret 在 YAML 中为绑定渠道/字段的 AES-256-GCM `enc:v1:` 密文，密钥为 `<path>/notification.key`、0600 普通文件。读取 API 只返回存在标记；敏感输入留空保留，显式清空才删除。不要在 YAML 填通知明文凭据；已有密文时密钥丢失/不匹配会阻止启动。

备份应先正常停止服务，再一起备份 YAML、**完整 state 目录**（SQLite、通知密钥、队列、时区等）和镜像根。只备份 YAML 或 SQLite 无法恢复通知凭据。在线备份需保证 SQLite 一致性，不能只复制写入中的数据库。忘记密码时修改 YAML 后重启。

## 下载与发布

服务仅公开配置中的发布源，不暴露整个磁盘根。工作目录为 `.mirrordock/work/<run-id>`，成功代位于 `.mirrordock/published/<uuid>`；发布指针先持久化再切换路由。旧代至少保留 24 小时，有活动引用不回收，缺失旧包可回退读取。首次启动可能校验并导入已有 dest 目录，部署后不要直接修改发布代。删除源撤销路由/计划，不删除镜像文件；活动、暂停、中断任务必须先取消才能改 dest。

发布路由保存代和引用，不为每个旧代永久打开文件根。请求在打开文件前取得 FD 配额，按需打开受限根，取得真实文件后立即关闭根；TCP 与文件请求按同一进程软限制分配额度，保留数据库/同步/日志余量。文件配额同时约束 GET、HEAD、目录和 HTTP/2 多流，饱和返回 503/Retry-After。

同步要求镜像目录实测支持 reflink：Linux 使用 FICLONE（生产 XFS 必须开启 reflink），macOS 开发环境使用 APFS 原生克隆；published 与 work 必须位于允许克隆的同一文件系统。克隆失败停止任务，不回退完整复制。多代中未变化的文件共享数据块，新增/替换的文件及被旧代保留的内容仍实际占用空间；首次同步仍需整套镜像容量。只有工作树元数据与修改身份均未变化才复用当前代，不生成新代；这一检查不读取文件正文。Rsync 保留单文件临时替换与 partial-dir，保护内部标记，不使用 delay-updates 或 inplace。

安全余量为文件系统总量的 1% 与 1 GiB 取大，任务启动另为每个活动 worker（含新任务）预留 512 MiB。空间不足时保持排队且不消耗运行预算，恢复后自动执行；运行中每 5 秒检查，低于安全余量停止任务并保留上一发布。此机制不是硬配额，检查间隔内的突增或外部写入仍可能触发 ENOSPC。持久化的成功/失败/超时/取消任务会清理 work，暂停/中断及未知目录保留；服务启动、终态及每 5 分钟触发回收，过期旧代仍遵守引用保护。系统设置的环境检查显示 reflink 支持/不支持/探测失败及原因，并显示实际文件系统容量、余量和目录数量；刷新会重新实测，不将各代逻辑大小相加当作物理占用。

公开目录可搜索/排序，不分页，最多列出 1000 项并提示截断；其他文件仍可用完整路径下载。无 JavaScript 时仍有已列出的链接。项目行展示状态、公开文件逻辑大小和上次/下次同步；GitHub 版本行展示大小。符号链接不回显目标，私有标记不可下载。

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

## 部署与目录

`docker build -t mirrordock .` 使用先 Vue 后 Go 的多阶段构建，EL9 运行层先应用已发布安全更新，再安装 rsync/DNF。Docker 与真实 EL 验收以当前验收文档为准。配置中的 path 和 mirror_root 可设为 `/var/lib/mirrordock` 与 `/srv/mirrors`。

国内网络可使用 `docker build --build-arg GOPROXY=https://goproxy.cn --build-arg ROCKY_MIRROR=https://mirror.nju.edu.cn/rocky -t mirrordock .`。参数选择构建依赖下载源，默认仍为 Go 官方代理和 Rocky 镜像列表，保留 Go 校验数据库与 RPM 签名检查；Rocky 源保留在镜像的软件源配置中，应用同步仍使用任务私有仓库。基础镜像加速由 Docker 引擎的 `registry-mirrors` 设置，例如按 [毫秒镜像说明](https://1ms.run/guide) 配置用户选择的源；这些设置不写入应用 YAML。

```sh
docker run --rm --name mirrordock -p 8080:8080 -e TZ=Asia/Shanghai \
  -v /absolute/config-dir:/etc/mirrordock \
  -v /absolute/state-dir:/var/lib/mirrordock \
  -v /absolute/mirror-dir:/srv/mirrors mirrordock
```

systemd 模板见 [deploy/mirrordock.service](deploy/mirrordock.service)，部署前创建服务用户、目录并赋权，按实际位置调整路径。低端口另需绑定权限。

目录基线只维护于 [目录规范.md](目录规范.md)，协作规则见 [AGENTS.md](AGENTS.md)，当前实现见 [IMPLEMENTATION.md](docs/IMPLEMENTATION.md)。

```text
cmd/mirrordock/           CLI 入口
internal/{config,observability,database,sync,runs,mirrors,notifications,app}/
web/                     唯一 npm/Vite 边界；Vue、E2E、embed.go
configs/                 注释案例；本地实例忽略
deploy/                  systemd 定义
scripts/                 favicon 与目录图标生成工具
docs/                    当前设计、实施、验收与本次计划
bin/ data/ .cache/        忽略的本机二进制、状态与缓存
test-results/            忽略的 Go/OCR/构建报告
```

| 基线规则 | 实际行为 | 原因与依据 | 适用范围 / 重新评估条件 |
| --- | --- | --- | --- |
| §7.1 配置入口与覆盖 | -f/--config；默认 cwd/config.yaml；无字段环境覆盖 | 用户确认的 YAML 协议 | 本程序；仅用户要求改变时评估 |
| §7.1 密钥注入 | GitHub Token 直接 YAML；通知凭据 YAML 密文；代理 URL 管理端回填 | 已确认配置与代理编辑协议 | 配置 API/任务；凭据协议改变时评估 |
| §11 工具必需路径 | web/embed.go 嵌入 web/dist | Go embed 与 Vite 的实际构建边界 | Web 资源；交付方式改变时评估 |
| §7.2 日志位置 | log.path 可为部署绝对路径或相对配置目录 | 已确认顶层 log 字段；验证与镜像/敏感路径隔离 | 程序日志；日志协议改变时评估 |
