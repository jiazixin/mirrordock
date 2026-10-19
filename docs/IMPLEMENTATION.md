# 实现与维护入口

当前架构与约束见 [DESIGN.md](DESIGN.md)，使用入口见 [README.md](../README.md)，配置、备份与部署细节见 [DEPLOYMENT.md](DEPLOYMENT.md)，构建、发布与测试见下文，可追溯验收见 [ACCEPTANCE.md](ACCEPTANCE.md)。目录基线只在 [目录规范.md](../目录规范.md) 维护。

## 数据与接口

数据库由 `internal/database` 启动建表/索引并写入结构版本 `PRAGMA user_version = 1`，版本更高的数据库拒绝打开；表结构变化须提升 `schemaVersion` 并编写迁移。运行数据以单条 JSON 聚合 attempts/events/脱敏快照；发布指针独立存储。历史保留，日志清理不删除事实。WAL/FULL 单写连接，分析走有界只读快照。`runs(source_id,id DESC)` 支持每源最新状态，不能用全局最近 50/100 条代替。

| 接口 | 用途 |
| --- | --- |
| GET /api/auth/status；POST /api/auth/login、logout | YAML 账号会话 |
| GET/PUT /api/config；POST /api/config/reload | 脱敏配置读取、revision 保存、重载 |
| GET /api/runs；GET /api/runs?latest=1 | 完整历史分页/筛选；每源最新状态 |
| GET /api/runs/{id}、/api/runs/{id}/logs | 详情；有界 offset 读取或完整日志下载 |
| POST /api/sources/{id}/run；POST /api/runs/{id}/{action} | 手动执行；pause/resume/cancel |
| POST /api/logs/clear | 仅清理终态日志 |
| GET /api/events、/api/system/info、/api/system/checks | 实时状态、运行环境、工具/权限、reflink 实测与镜像盘余量 |
| GET /api/analytics | 类型、7/30/90 天与源筛选聚合 |
| GET /api/notifications；PUT /api/notifications/{channel} | 密文存在标记；单渠道配置 |
| POST /api/notifications/{channel}/test、/api/notifications/timezone | 保存后的测试；浏览器时区 |
| GET /api/notifications/records | 通知发送历史分页 |

除 auth status/login 外管理 API 均鉴权；写操作同源校验。完整代理 URL 回填是已确认协议例外，Token/密码/通知凭据不返回。运行快照不保存代理/Token/通知密文。

来源 URL 与 GPG 公钥 URL 禁止用户信息、查询参数及片段。`internal/runs/snapshot.go` 负责运行快照的防御性脱敏。run_results 的自增 sequence 表示结果插入顺序，用于通知有界扫描，id 唯一约束继续保留首个不可恢复终态事实（中断不写结果队列）。

## 源码定位

- 配置及路径：`internal/config/config.go`；所有 dest 同时检查逻辑路径和解析后目录重叠。
- 任务与重试：`internal/runs/runs.go`；时间范围：`internal/app/location.go`；分析：`internal/runs/analytics.go`。
- Rsync/DNF 命令：`internal/sync/runner.go`；子进程和进度：`process.go`；GitHub：`github.go`。
- 文件与发布：`internal/mirrors/{mirrors,http,validate,summary}.go`；克隆/容量/无变化复用：`clone_{linux,darwin}.go`、`space_{linux,darwin}.go`、`storage.go`、`fingerprint.go`；公开目录：`listing.go` 与 listing HTML/CSS/JS/icons（模板按元素分行，行内内容保持单行以免引入可见空白）。
- 文件句柄：`internal/mirrors/descriptors.go` 与 `internal/app/connections.go` 分配同一进程预算；代根按需打开，不按历史代常驻。文件流与请求引用保持至请求结束。
- 凭据/发送队列：`internal/notifications`；独立程序日志：`internal/observability`；认证/服务生命周期：`internal/app`。
- Vue 请求与会话：`web/src/app`；业务页面：`features`；共享控件：`components`；所有页面共享格式化规则。

## 实施边界

GitHub 原始日志有资产结果，当前没有每资产独立数据库检索页。Reposync 未提供可靠包数/接收量，不推算百分比或统计。公开目录不分页，最多列出 1000 项；符号链接不回显目标。大小摘要在发布/恢复时生成，文件下载不扫描目录树。

程序不自动轮转日志、不删除历史、不过滤任意管理员 Webhook 为公共互联网地址、不自动提权；这些是当前运维协议。镜像发布代需保持不可变，数据与镜像根只能由受信任服务身份维护。Linux 性能、真实 EL/DNF、外部通知可达性、容器及断电恢复验收均以当次 ACCEPTANCE 证据为准。

`/api/system/checks` 的 `reflink.status` 为 supported/unsupported/error，`detail` 记录实测结果；`storage` 给出 work 文件系统 total/available/reserve/required（下一任务准入）、workers、当前路由 previous 数与数字 work 目录数，读取失败通过 error 返回。目录数不表示物理空间或所有已删除配置源的留存文件。生产同步要求可用 reflink；容量保护为提前停止策略，首次全量及增量峰值仍需实际容量规划。

审查与回归记录集中在 ACCEPTANCE，只描述当前代码的验证结果；更早的会话记录保留在 Git 历史中。

## 构建与版本

需要 Go 1.27.2、Node.js >=22.12、npm。部署 rsync 应为包含发行版安全补丁的 **3.1+** 版本；macOS 自带旧版本不支持所需参数。Reposync 使用 DNF 4、`dnf-plugins-core` 和开启 GPG 检查时所需的 `rpmkeys`；受控仓库已验证 Rocky 9 和 AlmaLinux 10.2，具体架构与范围见验收记录，不承诺兼容 DNF5。GitHub 下载不需要 gh。

从仓库根执行，必须先生成 Web，再编译 Go：

```sh
npm --prefix web ci
npm --prefix web run build
go build -trimpath -o bin/mirrordock ./cmd/mirrordock
./bin/mirrordock --version
```

发布 tag 采用 `v主版本.次版本.修订版本`，例如 `v1.1.0`、`v1.2.0`，预发布为 `v1.2.0-rc.1`；不使用 `1.1`、`v1.2` 等非规范 tag。普通构建不需要注入版本：Go 1.24 起会在可识别的 Git 仓库中将模块版本写入二进制，本项目通过 `debug.ReadBuildInfo().Main.Version` 读取。精确标准 tag 显示对应版本，非标签提交显示 Go 生成的伪版本，未提交修改保留 `+dirty`；额外显示 12 位提交 ID。应以包路径构建，并保留 `.git` 与对应 tag，不使用 `-buildvcs=false` 关闭此能力。Go 模块的 tag 必须匹配模块路径的主版本及子目录规则，见 [Go 官方版本说明](https://go.dev/ref/mod#versions)。

版本优先级为显式链接注入（非空且非 `dev`）→ Go 主模块版本（非空且非 `(devel)`）→ 12 位提交 ID → `dev`。显式覆盖仍保留可取得的 modified 状态；运行时不调用 Git。管理台“系统配置”和 `/api/system/info` 显示与 CLI 相同的实际构建版本。`--version` 和 `--help` 不读取配置或启动服务。交叉编译时 Web 产物不变，只给 Go 编译指定目标平台：

```sh
npm --prefix web run build
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o bin/mirrordock-linux-amd64 ./cmd/mirrordock
```

源码压缩包没有 Git 信息时可显式注入；以下 `v1.1.0` 是示例，必须替换为与源码对应的真实版本，不能把未知来源或未提交修改标为正式发行版：

```sh
npm --prefix web run build
go build -trimpath -ldflags='-X main.version=v1.1.0' -o bin/mirrordock ./cmd/mirrordock
```

### 发布

推送标准 tag（如 `git tag v1.2.0 && git push origin v1.2.0`，预发布 `v1.2.0-rc.1` 自动标为 Pre-release）触发 [.github/workflows/release.yml](../.github/workflows/release.yml)：先构建一次并核对 `--version` 恰为 `mirrordock <tag> (revision=<12 位提交>)`，不一致即中止；再由 GoReleaser v2.18.3 按 [.goreleaser.yaml](../.goreleaser.yaml) 先构建 Web，再以 `CGO_ENABLED=0 -trimpath -ldflags='-s -w'` 交叉编译，上传 GitHub Release。版本仍由 Go 从 Git tag 自动写入，发布构建不注入 `main.version`。

产物为 `mirrordock_<版本>_<系统>_<架构>.tar.gz`（含二进制、`LICENSE`、`README.md`、`README.en.md`、`docs/DEPLOYMENT.md`、`configs/config.example.yaml`、`deploy/mirrordock.service`）与 `checksums.txt`（SHA-256）。目标：linux amd64、arm64、ppc64le、s390x、riscv64、loong64，darwin amd64、arm64。32 位目标无法编译，不发布；除 linux amd64/arm64 与 darwin 外的架构仅交叉编译，未经实机运行。

本地预演（不发布，输出在忽略的 `bin/release/`）：

```sh
go install github.com/goreleaser/goreleaser/v2@v2.18.3
goreleaser check   # 需要 Git remote
goreleaser release --snapshot --clean
```

### Web 嵌入与资源

`web/dist/` 是唯一 Web 输出，由 `web/embed.go` 嵌入二进制，不提交 Git。干净检出直接执行 Go 构建或全量测试会因缺少嵌入文件失败。容器构建也先构建 Web。Docker 上下文不包含 Git，需传入 VERSION；未传时显示 dev，容器构建不附带 Git dirty 元数据。管理页面按路由加载，资源随二进制发布，无 CDN 或远程字体。

Logo 唯一源为 `web/src/assets/logo.svg`；`web/public/favicon.ico`（16–256 px 多尺寸 ICO）由它导出并提交，修改 logo 后用任意图像工具重新导出该文件再构建，构建本身不依赖图像工具。公开目录的 Phosphor Regular 图标以 SVG 路径形式收录在 `internal/mirrors/listing-icons.html`，文件头记录上游固定提交与 MIT 授权；新增或替换图标时从同一上游的 `assets/regular/` 手工复制，只保留路径元素且颜色仅用 `currentColor`。

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

未设置测试 rsync 时部分协议测试会跳过；测试通过不代表跳过项已验证。报告位于根 `test-results/` 和 `web/test-results/`，均忽略。

### CI

GitHub Actions 配置只维护在 [.github/workflows/ci.yml](../.github/workflows/ci.yml)，推送 main 与 Pull Request 时运行，Action 均按提交 SHA 固定：

- Web：`npm ci --ignore-scripts`、`format:check`、构建。
- Go（ubuntu-24.04）：先构建 Web，再 `go mod verify`、gofmt、`go vet`（含 `GOOS=darwin` 交叉检查）、`go test -race -count=1 ./...`。不设 macOS 运行器，macOS 只做交叉 vet 与交叉编译。
- E2E（ubuntu-24.04）：构建测试二进制后运行全部 Playwright 用例。
- GoReleaser：`goreleaser check` 与 `release --snapshot --clean`，确认全部发布目标可交叉编译和打包。

Go 与 E2E 作业先执行组合 Action [.github/actions/test-env](../.github/actions/test-env/action.yml)：Linux 运行器为 ext4，它挂载开启 reflink 的稀疏 XFS 镜像并设为 `TMPDIR`，并以系统 rsync 设置 `MIRRORDOCK_TEST_RSYNC`，因此 CI 不跳过 rsync 用例。该 Action 只在 Actions 中运行；本地等价命令即上文“开发验证”。CI 只覆盖 GitHub 托管运行器，不等于目标 EL 发行版实机验收。

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
