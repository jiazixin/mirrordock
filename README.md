# MirrorDock · 源仓

Go **1.27.1** + Vue 3 的软件源同步与 HTTP/HTTPS 镜像服务。Linux 是生产目标平台，macOS 可开发和验证。rsync 调用系统工具，GitHub Release 使用 `google/go-github` 和 Go 下载器，可选 DNF 4 reposync；无需 gh。

提供同步源管理、手动/cron 执行、srcN 失败切换、累计超时、暂停/继续、SQLite 运行记录、成功/失败分页、原始日志详情与手动清理。公开镜像直接使用 `/docker-ce/`、`/almalinux/` 等 dest 路径；管理台在 `/admin/`。页面参考用户截图，采用新的钴蓝配色和 MirrorDock Logo。

## 安装与构建

需要 Go 1.27.1、Node.js >=22.12（建议 Node 24）、npm。生产 rsync 使用包含发行版安全补丁的 **3.1+** 版本；启用 reposync 时需要 EL8/EL9 的 DNF 4 和 `dnf-plugins-core`。macOS 自带的旧 rsync 不支持本项目参数，请使用现代版本。

从仓库根执行，必须先构建真实 Vue 输出，再编译 Go：

```sh
npm --prefix web ci
npm --prefix web run build
mkdir -p bin
go build -trimpath -o bin/mirrordock ./cmd/mirrordock
```

`web/dist/` 是唯一 Web 构建输出，由 `web/embed.go` 嵌入二进制，不提交 Git。干净检出直接 `go build` / `go test ./...` 会因没有嵌入文件失败，因此这些入口都需要上述 Web 预构建；容器构建已显式包含这一步。

Logo 的唯一设计源为 `web/src/assets/logo.svg`；侧栏与登录页直接使用矢量图。`web/public/favicon.ico` 从同一 SVG 生成，包含 16、32、48、64、128、256 像素六个尺寸，管理台与公开目录使用相同图标。修改 Logo 后用安装了 Pillow 的 Python 执行 `python3 scripts/generate-favicon.py`，再按上述顺序构建；普通构建使用已保存的 ICO，不需要 Python。重启新二进制后页面与图标更新。

管理台采用参考图风格的系统字体和标准 400/500/600/700 字重，所有选择框统一为自定义组件；支持方向键、Home/End、Enter、Escape、Tab 和点击外部关闭，菜单随视口定位并避开弹窗裁切。

## 首次启动

完整源案例和每个字段的注释在 [configs/config.example.yaml](configs/config.example.yaml)。开始本机试用时可以创建空源配置，之后从页面新增：

```sh
cat > configs/config.local.yaml <<'YAML'
listen: 127.0.0.1
port: 8080
path: ../data/state
mirror_root: ../data/mirrors
tls:
  cert: ""
  key: ""
settings:
  task_timeout: 8h
  rsync:
    io_timeout: 5m
    connect_timeout: 30s
    limit_mb: 0
  github:
    release_count: 5
    token: ""
    proxy: ""
sources: []
YAML
./bin/mirrordock --config configs/config.local.yaml
```

访问 `http://127.0.0.1:8080/admin/`，使用启动终端打印的一次性初始化码设置至少 12 字符的管理员密码。管理员密码以 bcrypt 哈希存入 SQLite；初始化码不会通过 API 返回。重启后使用原密码登录，会话需要重新建立。

新增源后执行一次同步，成功校验才发布。示例中的 Docker CE、Kubernetes、AlmaLinux 备用域名需要替换为自己的上游。示例有已开启的自动计划；正式采用案例配置前先确定目录、上游与计划。程序不会依据源名称替你选择 rsync/reposync。

## 配置约定

`-f <文件>` 和 `--config <文件>` 等价；两者同时指定不同文件会报错。没有参数读取工作目录 `config.yaml`，文件不存在就报错，不自动覆盖或生成。所有相对路径以被选中的配置文件目录为基准，数据库固定为 `<path>/data.sqlite`，日志在 `<path>/logs/<run-id>/<attempt>.log`。

用户已确定的 YAML 协议优先于目录规范的默认协议：不用 `CONFIG_FILE` 或环境变量覆盖字段，GitHub 凭证直接放 `settings.github.token`。没有 version、timezone、maxconcurrent、exec、token_env 或 records 字段。进程启动环境/系统时区决定调度与显示时区；可在启动环境设置 `TZ=Asia/Shanghai`，不使用浏览器自动时区。

- `listen`、`port`、`path`、`mirror_root`、TLS 修改后需要重启。其余修改对新任务/计划生效，当前任务保留脱敏配置快照。Token 在每次实际执行时读取当前配置。
- 页面写回 YAML，保留现有注释，通过 revision 防止覆盖另一位管理员/磁盘编辑；保存使用同目录临时文件、fsync 与 rename。外部修改后点击“重新载入配置”。文件及其父目录必须允许程序身份写入，不能只挂载不可替换的单个配置文件。
- rsync 来源使用 `src1`、`src2`、`src3` 等，按数字升序尝试；连接/网络类错误切换，磁盘与权限类错误停止。失败切换和暂停继续共享默认 8h 累计运行预算，排队/暂停不计时，超时后先 TERM，最多 10 秒后 KILL 进程组。
- `rsync.delete` 和保留 links/times/perms/hardlinks 默认 true。`limit_mb` 是每个上游任务的十进制 MB/s，0 不限速；不限制公开下载。UID/GID 可只填一项，0 有效；跨 UID 映射通常需要 root，程序不自动提权。
- GitHub 最近 N 个 Release 按发布时间从新到旧，源内 `release_count: 0`/省略继承系统设置；仅下载附件，不请求自动源码 ZIP/TAR。glob 可限制架构。Token 读取 API 不返回值，留空输入框保留已有值，页面提供显式清空选项。
- GitHub 的 `settings.github.proxy` 支持 `http://`、`https://`、`socks5://`、`socks5h://`，API 与附件共用；空字符串直连，不读取代理环境变量。sing-box 同机部署建议填其 HTTP/mixed 入口，如 `http://127.0.0.1:8081`，端口换成实际监听值；异机部署填可达地址。GitHub 的 HTTPS 会通过 HTTP CONNECT 隧道，`https://` 表示代理入口本身使用 TLS，需有效证书。支持 URL 中填写用户名/密码，特殊字符须 URL 编码；读取 API/运行快照不返回完整代理地址，页面留空保留、勾选清空后直连。代理不可用不会绕过代理；设置修改对下一次实际执行生效，不改变已建立的下载连接。
- cron 使用五字段和程序时区；同源已有排队、运行、暂停或中断任务时跳过重叠触发。内置两个同步 worker，不增加并发配置字段。
- 数据库、配置、TLS 私钥与镜像根必须隔离；dest 必须规范相对目录，不能包含保留的 admin/api/favicon.ico/内部目录或互相嵌套。服务仅公开配置中的已发布源，不把整个磁盘根暴露出来。

TLS 两项留空提供 HTTP，填齐提供同端口 HTTPS。证书链和私钥必须有效且匹配；配置或证书更换后重启。Go 原生 TLS 支持 HTTP/2，HTTPS 不宣称使用 sendfile。

## 下载与发布

`/` 为公开目录首页，`/<dest>/` 直接提供镜像，支持 GET、HEAD、单段/多段 Range、ETag 与条件请求；未知镜像文件返回 404。管理 API 使用 Cookie 鉴权及同源写入验证。公开文件强制附件下载、nosniff 和 sandbox，私有工作目录、续传片段、数据库和配置不对外提供。

同步到 `.mirrordock/work/<run-id>`，基于当前代 reflink 或独立复制；成功校验并同步文件和目录数据，预验证文件根及公开链接后提交发布指针、替换路由；提交失败回滚链接和暂存。首次导入已有 dest 目录会校验后移动到私有发布代，并保留同路径链接。旧版本自替换起至少保留 24h，活动请求固定文件根，缺失的旧包可以回退读取；失败任务不会覆盖当前公开代。删除源配置保留文件，运维删除已发布数据前需停止程序并确认引用。

Linux HTTP/1.1 的真实文件与 `ReaderFrom` 委托链保留 sendfile 条件；正文交给内核页缓存，下载热路径不查 SQLite、不读 YAML、不计算整文件哈希、不动态压缩。下载池和元数据缓存有界，连接使用无进展监测。详见 [实现说明](docs/IMPLEMENTATION.md) 和 [验收记录](docs/ACCEPTANCE.md)。本机回环速度不等于实际服务器/磁盘/网络吞吐保证。

## 开发与测试

开发前也先生成 `web/dist`，随后可分别运行：

```sh
go run ./cmd/mirrordock -f configs/config.local.yaml
npm --prefix web run dev
```

Vite 在 `http://127.0.0.1:5173/admin/`，将 `/api` 代理到 8080；浏览器以相同端口 Origin 发起写入，后端开发代理需要保留该同源关系。正式构建页面由 Go 自己提供。

```sh
npm --prefix web run build
go vet ./...
go test -race ./...
go test ./internal/mirrors -run '^$' -bench BenchmarkPublicHTTP -benchmem
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/mirrordock-linux-amd64 ./cmd/mirrordock
```

Go 测试使用临时目录和本地 HTTP 上游。真实 rsync 协议与浏览器 E2E 需要显式提供测试可执行文件，始终使用隔离 daemon，绝不同步案例中的完整发行版：

```sh
MIRRORDOCK_TEST_RSYNC=/usr/bin/rsync go test -race ./...
# 先构建 bin/mirrordock 与 Web，并安装 Playwright 的 Chromium
(cd web && npm exec -- playwright install chromium)
MIRRORDOCK_TEST_RSYNC=/usr/bin/rsync npm --prefix web run test:e2e
```

该变量只供测试选择 fixture daemon，不是产品的 rsync exec 配置。Web 报告在 `web/test-results` / `web/playwright-report`，Go/OCR 报告在根 `test-results`，都不提交。当前仓库未连接外部 CI，以上为 CI 的本地等价命令；没有虚构已运行的远程流水线。

## 部署与备份

容器示例：`docker build -t mirrordock .`。多阶段显式先构建 Vue，再用 Go 1.27.1 编译，EL9 运行层包含 rsync 和 DNF reposync。启动时挂载整个配置目录和独立数据/镜像目录，例如将配置的 path 设为 `/var/lib/mirrordock`，mirror_root 设为 `/srv/mirrors`：

```sh
docker run --rm --name mirrordock -p 8080:8080 -e TZ=Asia/Shanghai \
  -v /absolute/config-dir:/etc/mirrordock \
  -v /absolute/state-dir:/var/lib/mirrordock \
  -v /absolute/mirror-dir:/srv/mirrors \
  mirrordock
```

systemd 定义在 [deploy/mirrordock.service](deploy/mirrordock.service)。默认以 mirrordock 用户运行；预先创建配置、数据和镜像目录并赋予所需读写权限。不要在这一身份下配置映射为其他 UID 后再期待自动提权。根据实际部署修改路径与用户，端口 <1024 另需相应绑定权限。生产管理会话需要保密时启用上述 TLS 配置。

记录和任务日志永久保留，页面“清理日志”只删除已结束尝试的原始日志，保留历史、状态和错误摘要；活动与暂停日志不会删除。为数据目录和暂存/发布代预留容量，reflink 不可用时复制会占用额外正文空间。备份最简单的方式是先正常停止服务，再一起备份 YAML、完整 state 目录和镜像根；运行期间需采用 SQLite 一致性备份，不能只复制正在写入的 data.sqlite。忘记管理员密码时可停服后用 SQLite 删除 app_state 的 admin_password 行，下次启动重新生成初始化码。

## 实际目录

```text
cmd/mirrordock/           CLI 入口
internal/{config,database,sync,runs,mirrors,app}/
migrations/              唯一 SQL schema 与 embed
configs/                 注释案例；本地配置被忽略
deploy/                  systemd 定义
scripts/                 Logo 衍生 favicon 生成工具
docs/                    设计、计划、实现与验收
web/                     独立 npm/Vite 边界、Vue 源码、e2e、embed.go
bin/                     本地二进制（忽略）
data/                    本地持久数据（忽略）
.cache/                  可重建测试工具缓存（忽略）
test-results/            Go/OCR 运行报告（忽略）
```

目录例外：无额外业务目录。`web/embed.go` 用于嵌入 Vite 唯一输出，`migrations/embed.go` 嵌入唯一 SQL 来源，均属于实际使用的 Go 包；配置文件选择和直接 YAML Token 协议是用户明确确认的协议例外，详见“配置约定”。协作规则见 [AGENTS.md](AGENTS.md)，目录基线仍只在 [目录规范.md](目录规范.md) 维护。
