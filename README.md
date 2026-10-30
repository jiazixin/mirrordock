<p align="center">
  <img src="web/src/assets/logo.svg" width="88" alt="MirrorDock logo">
</p>

<h1 align="center">MirrorDock · 源仓</h1>

<p align="center">
  自托管的软件源同步与镜像服务：一个二进制，同步 rsync / DNF / GitHub Release，直接对外提供下载。
</p>

<p align="center">
  <a href="https://github.com/jiazixin/mirrordock/actions/workflows/ci.yml"><img src="https://github.com/jiazixin/mirrordock/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="https://github.com/jiazixin/mirrordock/releases/latest"><img src="https://img.shields.io/github/v/release/jiazixin/mirrordock?sort=semver" alt="Release"></a>
  <img src="https://img.shields.io/badge/Go-1.27.3-00ADD8?logo=go&logoColor=white" alt="Go 1.27.3">
  <img src="https://img.shields.io/badge/platform-Linux%20%7C%20macOS-4c8b3f" alt="Linux | macOS">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="License: Apache-2.0"></a>
</p>

<p align="center">
  <b>简体中文</b> · <a href="README.en.md">English</a>
</p>

---

MirrorDock 是 Go 后端加内嵌 Vue 3 管理台的单二进制程序，用来搭建内网或公开的软件源镜像站。它调用系统 `rsync`、可选的 `dnf reposync`，并用 Go 下载 GitHub Release 附件。同步结果校验通过后，以不可变“发布代”原子切换上线。镜像直接从 `/<dest>/` 下载，管理台在 `/admin/`，管理 API 在 `/api/`。

> [!IMPORTANT]
> - **同步要求镜像所在文件系统支持 reflink**（开启 reflink 的 XFS、macOS APFS）。ext4、NFS 等环境无法同步，程序不会回退为完整复制。
> - **镜像根即公开目录**：`mirror_root` 下未被配置为同步源的文件也会被列出并可下载，私有内容不要放进镜像根。

## 功能特性

- **三种同步方式**：rsync（多个备用来源依次尝试，支持全局或按源代理）、DNF 4 `reposync`、GitHub Release（只下载 assets，可按 glob 筛选，支持批量添加仓库）。
- **调度与任务控制**：手动或 cron 触发；按类型限制并发；累计超时预算；自动重试并退避；任务可暂停、继续、取消；服务重启后自动续跑被中断的任务。
- **安全发布**：在工作目录完成同步与校验后才切换到新发布代，失败时继续提供上一代；多代之间用 reflink 共享未变化的数据块。
- **高性能下载**：HTTP/1.1 走 sendfile，支持 Range / HEAD / 条件请求；下载热路径不访问数据库、不逐块写日志、不动态压缩。
- **管理台**：概览时间轴、运行记录与原始日志、Rsync / Reposync / Release 运行分析、环境检查（含 reflink 实测与磁盘余量）。
- **通知**：企业微信、钉钉、Telegram、Bark、通用 Webhook；凭据在 YAML 中以 AES-256-GCM 密文保存。
- **YAML 为配置真源**：页面保存时保留注释，检查 revision 后原子写入；SQLite 保存运行历史。

## 运行要求

| 项目 | 要求 |
| --- | --- |
| 操作系统 | 生产环境为 Linux；macOS 用于开发和本机验证 |
| 文件系统 | 镜像根需支持 reflink，且 work 与 published 位于同一文件系统 |
| rsync | 3.1 及以上，并包含发行版的安全补丁（macOS 自带版本过旧） |
| Reposync（可选） | DNF 4、`dnf-plugins-core`，开启 GPG 检查时还需要 `rpmkeys`；不承诺兼容 DNF5 |
| 从源码构建 | Go 1.27.3、Node.js ≥ 26.12.1、npm |

## 快速开始

### 1. 下载预编译版本

在 [Releases](https://github.com/jiazixin/mirrordock/releases/latest) 下载对应平台的包。提供 linux amd64 / arm64 / ppc64le / s390x / riscv64 / loong64 与 darwin amd64 / arm64；其中 linux amd64、arm64 和 darwin 之外的架构只做过交叉编译，未在实机上运行过。

```sh
curl -LO https://github.com/jiazixin/mirrordock/releases/download/v1.0.0/mirrordock_1.0.0_linux_amd64.tar.gz
curl -LO https://github.com/jiazixin/mirrordock/releases/download/v1.0.0/checksums.txt
sha256sum --ignore-missing -c checksums.txt
tar -xzf mirrordock_1.0.0_linux_amd64.tar.gz
./mirrordock --version
```

包内包含二进制、LICENSE、本 README、英文 README、[部署与运维参考](docs/DEPLOYMENT.md)、带注释的 [配置示例](configs/config.example.yaml) 和 [systemd 单元](deploy/mirrordock.service)。

> macOS 包只有 ad-hoc 签名、未经公证。用浏览器下载后如被 Gatekeeper 拦截，可执行 `xattr -d com.apple.quarantine mirrordock`。

### 2. 编写最小配置并启动

先用一个不含同步源的配置试运行（相对路径以配置文件所在目录为基准）：

```sh
cat > config.yaml <<'YAML'
listen: 127.0.0.1
port: 8080
path: ./data            # SQLite、任务日志与通知密钥
mirror_root: ./mirrors  # 镜像根，对外公开
auth:
  username: admin
  password: 请替换为你的密码
sources: []
YAML
mkdir -p mirrors
./mirrordock -c config.yaml   # 不带 -c 时读取 CONFIG_FILE，再到 configs/config.local.yaml
```

打开 `http://127.0.0.1:8080/admin/`，用 YAML 里的账号登录，在页面上添加同步源并手动执行一次同步。公开目录首页是 `http://127.0.0.1:8080/`。

> [!WARNING]
> [config.example.yaml](configs/config.example.yaml) 中的源只是字段示例，其中有启用了 cron 的完整发行版同步。请逐项确认上游地址和计划后再启用，不要直接拿完整示例来测试。

### 3. 从源码构建

在仓库根目录执行。必须先构建 Web，因为 `web/embed.go` 会把 `web/dist/` 嵌入二进制：

```sh
npm --prefix web ci
npm --prefix web run build
go build -trimpath -o bin/mirrordock ./cmd/mirrordock
./bin/mirrordock --version
```

交叉编译时 Web 产物不变，只需给 `go build` 加目标平台：`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o bin/mirrordock-linux-amd64 ./cmd/mirrordock`。

## 配置

完整字段与注释见 [configs/config.example.yaml](configs/config.example.yaml)，行为细节见 [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)。要点如下：

- `-c <文件>` 与 `--config <文件>` 等价，同时指定不同文件报错。选择顺序为 `--config` > 环境变量 `CONFIG_FILE` > 工作目录下的 `configs/config.local.yaml`；`CONFIG_FILE` 设置为空或全空白时报错。文件不存在就报错，不会自动生成；默认文件缺失同样报错，因为内置默认值不含必填的 `auth.username` 与 `auth.password`。相对的 `CONFIG_FILE` / `-c` 路径以进程工作目录为基准。
- 配置与校验错误为英文，并以字段路径开头，例如 `port: must be between 1 and 65535`、`sources.0.dest: must be a clean relative directory`（源按列表下标定位）。管理 API 的错误响应为 `{"error": {"code", "message", "details"}}`：配置字段错误为 `invalid_argument`，并在 `details.fields` 中给出 `path` 与 `reason` 原因码；`precondition_failed` 表示配置已被他人修改（412，`If-Match` 不是当前 revision）；`config_malformed` 表示 YAML 格式无效。完整约定见 [接口约定](docs/API.md)。管理台按错误码显示中文提示（`web/src/app/config-errors.ts`），未登记的原因码显示中文通用提示。
- 相对路径以配置文件所在目录为基准。数据库为 `<path>/data.sqlite`，任务日志为 `<path>/logs/<run-id>/<attempt>.log`。
- 除 `CONFIG_FILE` 选择文件外，不支持用环境变量覆盖配置项。调度、管理台和公开目录使用程序启动时的时区，可在启动环境设置 `TZ=Asia/Shanghai`。
- `listen`、`port`、`path`、`mirror_root`、`tls`、`auth`、`log` 修改后需重启；同步源、计划、通知等可在页面修改，保存即生效。
- 管理员密码、GitHub Token 和通知凭据不会出现在读取 API 中。**例外**：按已确认的协议，GitHub / Rsync 的完整代理 URL 会在登录后的页面上明文回填，但其中的认证信息不进入运行快照、审计或日志。
- 填写 `tls.cert` 与 `tls.key` 后在同一端口提供 HTTPS（支持 HTTP/2，但不走 sendfile）。

## 部署

### systemd

```sh
sudo useradd --system --home-dir /var/lib/mirrordock --shell /usr/sbin/nologin mirrordock
sudo install -m 0755 mirrordock /usr/local/bin/mirrordock
sudo install -d -o mirrordock -g mirrordock -m 0750 /etc/mirrordock /var/lib/mirrordock /srv/mirrors
# 写入 /etc/mirrordock/config.yaml：path: /var/lib/mirrordock，mirror_root: /srv/mirrors，并替换密码
sudo install -m 0644 deploy/mirrordock.service /etc/systemd/system/mirrordock.service
sudo systemctl daemon-reload
sudo systemctl enable --now mirrordock
```

页面保存配置时会写回 YAML，所以配置目录必须允许服务用户写入。`/srv/mirrors` 应位于开启 reflink 的 XFS 上。绑定 1024 以下端口需要额外授权。

### Docker

```sh
docker build --build-arg VERSION=v1.0.0 -t mirrordock .
docker run --rm --name mirrordock -p 8080:8080 -e TZ=Asia/Shanghai \
  -v /absolute/config-dir:/etc/mirrordock \
  -v /absolute/state-dir:/var/lib/mirrordock \
  -v /absolute/mirror-dir:/srv/mirrors mirrordock
```

镜像默认以 UID/GID 10001 的非 root 用户运行，挂载目录需预先授权（如 `chown -R 10001:10001 <目录>`）。`VERSION` 必须与所构建的源码一致。国内构建加速参数见 [部署参考](docs/DEPLOYMENT.md#容器与-systemd-部署)。

### 数据与备份

持久数据包括 YAML 配置、`path` 指向的整个 state 目录（SQLite、`notification.key` 等）和镜像根。备份时先正常停止服务，再把三者一起备份。只备份 YAML 或 SQLite 无法恢复通知凭据。忘记密码时修改 YAML 后重启即可。

## 开发

```sh
npm --prefix web ci
npm --prefix web run format:check
npm --prefix web run build
go vet ./...
go test -race -count=1 ./...
```

E2E 测试与真实 rsync 测试需要现代 rsync 和测试二进制，命令见 [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md#开发验证)。推送到 `main` 和 Pull Request 时，[CI](.github/workflows/ci.yml) 会运行 Web 构建、Go 检查与测试、Playwright E2E，以及 GoReleaser 快照构建。

## 版本与发布

- 版本号来自 Git tag，格式为 `v主版本.次版本.修订版本`（预发布如 `v1.2.0-rc.1`）。Go 构建时自动写入模块版本，非 tag 提交显示伪版本，有未提交修改时带 `+dirty`。
- 查询版本：`mirrordock --version`，输出形如 `mirrordock v1.2.0 (revision=0123456789ab)`（伪版本已含提交号，不再附 revision；有未提交修改时附 `modified`）。`--version` 与 `--help`（英文）输出到 stdout，不读取配置，也不启动服务。
- 退出码：0 成功或收到 SIGINT / SIGTERM 后正常关闭，2 参数错误，1 配置、启动、运行或关闭失败。参数错误以 `mirrordock: <错误>` 写入 stderr 并另起一行附 `Run 'mirrordock --help' for usage.`；配置与日志初始化失败为一行 `mirrordock: config: <错误>` 或 `mirrordock: log: <错误>`。日志就绪后的失败只写入程序日志，不再另写 stderr。
- 程序日志记录 `service_starting`（含 `version`）、监听成功后的 `service_listening`（含 `listen`）、开始关闭时的 `service_stopping` 和正常退出前的 `service_stopped`；失败为 `service_failed` / `service_shutdown_failed`。第一次信号开始正常关闭（HTTP 排空最多 30 秒，超时则强制关闭连接并记为关闭失败），第二次信号立即终止进程。
- 源码包没有 `.git` 时，可显式注入与源码一致的版本：`go build -trimpath -ldflags='-X main.version=v1.0.0' -o bin/mirrordock ./cmd/mirrordock`。
- 推送标准 tag 会触发 [Release 工作流](.github/workflows/release.yml)：先核对 `--version`，再由 GoReleaser 构建全部目标，并上传 tar.gz 与 `checksums.txt`。

细节见 [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md#构建与版本)。

## 文档

| 文档 | 内容 |
| --- | --- |
| [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) | 部署与运维参考：配置协议、执行器、日志/TLS、通知、备份、下载与发布 |
| [docs/DESIGN.md](docs/DESIGN.md) | 当前架构与设计决策（含 ADR-010 下载性能） |
| [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md) | 接口、源码定位、构建、发布、测试与 CI |
| [docs/API.md](docs/API.md) | 错误响应、任务错误与事件代码、通知记录代码的契约 |
| [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md) | 验收记录、审查结果与未验证项 |
| [configs/config.example.yaml](configs/config.example.yaml) | 带注释的完整配置示例 |

## 项目结构

```text
cmd/mirrordock/           CLI 入口
internal/{config,observability,database,sync,runs,mirrors,notifications,app}/
web/                     唯一 npm/Vite 边界；Vue、E2E、embed.go
configs/                 注释案例；本地实例忽略
deploy/                  systemd 定义
docs/                    部署、设计、实施与验收
.github/                 GitHub Actions CI、tag 发布与测试环境组合 Action
.goreleaser.yaml         GoReleaser 多目标发布配置
bin/ data/ .cache/        忽略的本机二进制（含 bin/release/ 发布预演）、状态与缓存
test-results/            忽略的 Go/OCR/构建报告
```

目录基线只在 [目录规范.md](目录规范.md) 维护，协作规则见 [AGENTS.md](AGENTS.md)。本项目的目录例外：

| 基线规则 | 实际行为 | 原因与依据 | 适用范围 / 重新评估条件 |
| --- | --- | --- | --- |
| §3 根目录 README | 中文 `README.md` 为主入口，英文翻译为 `README.en.md` | 用户要求中英文 README | 两份同步维护；放弃英文版时评估 |
| §7.1 配置入口与覆盖 | -c/--config > CONFIG_FILE > configs/config.local.yaml；无字段环境覆盖 | 用户确认的 YAML 协议 | 本程序；仅用户要求改变时评估 |
| §7.1 密钥注入 | GitHub Token 直接 YAML；通知凭据 YAML 密文；GitHub/Rsync 全局与按源代理 URL 管理端回填 | 已确认配置与代理编辑协议（Rsync 代理沿用 GitHub 代理样式） | 配置 API/任务；凭据协议改变时评估 |
| §11 工具必需路径 | web/embed.go 嵌入 web/dist | Go embed 与 Vite 的实际构建边界 | Web 资源；交付方式改变时评估 |
| §4 `bin/` 只放可执行文件 | GoReleaser 输出（二进制、tar.gz、checksums、元数据）在 `bin/release/` | 默认根 `dist/` 未被忽略会让 Go 标记 dirty，且 `dist/` 保留给 Node/Web 产物 | 发布构建；更换发布工具时评估 |
| §7.2 日志位置 | log.path 可为部署绝对路径或相对配置目录 | 已确认顶层 log 字段；验证与镜像/敏感路径隔离 | 程序日志；日志协议改变时评估 |

## 代码例外

本项目长期偏离 [代码规范.md](代码规范.md) 及 [Go代码规范.md](Go代码规范.md) 的做法（代码规范第 9 节）：

| 规则 | 实际做法 | 原因与依据 | 适用范围 | 重新评估条件 |
| --- | --- | --- | --- | --- |
| Go代码规范 §7 配置为值类型，入口为 `Load(src Source) (Config, error)` | 运行期可编辑配置使用 `*Manager` 而非值类型 `Config`：入口为 `config.Open(src config.LoadSource) (*config.Manager, error)`，Manager 提供当前值、启动值、重载与写回 | 管理台修改配置后热加载并写回 YAML（保留注释、检查 revision、原子写入）；Go代码规范 §7 允许把运行期可编辑配置记录为项目例外 | `internal/config` 及持有 Manager 的 `cmd/mirrordock`、`app`、`runs`、`notifications` | 不再支持页面修改配置时 |
| Go代码规范 §6 安全响应头 `Content-Security-Policy: default-src 'self'; …` | 管理台页面（`/admin/`）使用 `style-src 'self' 'unsafe-inline'`、`img-src 'self' data:` 等放宽后的策略；`/api/` 与 `/healthz` 使用规范默认值 | Vue 的样式绑定输出内联 style，图标使用 data URI；脚本仍只允许同源构建产物 | `internal/app/app.go` 的 `adminContentSecurityPolicy` | 管理台不再使用内联样式时 |
| Go代码规范 §7 加载来源类型名 `Source` | 类型名为 `config.LoadSource`，字段与规范的 `Source` 相同（`Path`、`Explicit`、`WorkDir`、`LookupEnv`） | `config.Source` 已是镜像同步源的领域类型，被 20 个文件使用 | `internal/config` | 同步源类型改名时改回 `Source` |
| Go代码规范 §7 `func (c Config) Validate() error` | `func (c Config) Validate(file string) error`，只校验、不修改，参数为配置文件路径 | 配置中的相对路径以配置文件目录为基准；校验要解析符号链接，检查镜像根与数据、日志、配置、TLS 文件隔离，并加载 TLS 证书 | `internal/config` | 配置不再含相对文件路径时 |
| 代码规范 §3.1 用户文案由客户端按代码映射 | 通知正文（企业微信、钉钉、Telegram、Bark、Webhook 及测试通知）与公开镜像目录页（`internal/mirror/listing.*`）由服务端生成中文 | 通知发往外部渠道、目录页由服务端直接渲染给匿名用户，二者都没有可做映射的客户端；管理 API 与任务记录已按 [接口错误约定](docs/API.md) 只返回代码 | `internal/notification/message.go`、`service.go` 的测试通知、`internal/mirror/listing.*` | 通知或公开目录改为多语言或由前端渲染时 |

## 参与贡献

欢迎提交 Issue 和 Pull Request。提交前请：

1. 先阅读 [AGENTS.md](AGENTS.md) 与 [目录规范.md](目录规范.md)，新文件按其中的目录规则放置；
2. 在本地运行上文“开发”中的检查，前端改动还需运行 `npm --prefix web run format:check` 和相关 E2E；
3. 测试只使用临时目录、隔离端口和可控上游，不要对真实发行版源运行完整同步。

## 许可证

本项目基于 [Apache License 2.0](LICENSE) 开源。公开目录内嵌的 Phosphor 图标遵循其自带的 MIT 许可（见 `internal/mirror/listing-icons.html`）。
