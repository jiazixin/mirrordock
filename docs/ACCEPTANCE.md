# 上线前验收记录

验证日期：**2026-10-02 至 2026-10-03**。范围为当前完整工作区，包含开始时已存在的后端统计、时间筛选、共享开关和 UI 对齐改动，以及本次新增修复；保留用户配置协议与已有修改。未推送、部署、发送外部消息或启动完整发行版同步。

**本机代码、隔离 Linux 与 Rocky 容器验证通过，生产环境验收仍有待完成项。** 本文替代原先按会话追加的重复记录；历史输出仍留在被忽略的本机 test-results 中，不作为本次通过证据。

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

1. **Linux 实机**：隔离 Linux arm64 虚拟机已观测 sendfile，并验证 HTTP/HTTPS/HTTP2、并发 Range；目标服务器仍需验证慢客户端、冷/热缓存、同步下载并行时吞吐/CPU/RSS/FD/管理延迟。虚拟机与本机合成测试不能替代生产硬件测量。
2. **目标 EL8/EL9 / DNF4**：Rocky 9 aarch64 已完成真实工具与受控 RPM/GPG 仓库、客户端安装验证；EL8、Linux amd64 实际发行版和目标仓库全量同步仍待部署环境验证。未运行示例完整发行版同步。
3. **目标容器与 systemd**：Linux arm64 镜像拉取/多阶段构建/运行/退出已经实际完成；生产服务身份、宿主挂载、TLS、systemd 沙箱与权限仍需目标环境验收，Linux amd64 容器未运行。
4. **真实通知与故障恢复**：已验证本地受控接收端，未发送外部消息。正式渠道可达性、断电/磁盘满/存储 fsync 故障与备份恢复需部署环境演练。
5. **已知模块告警处置**：openpgp 告警保留为不适用记录，项目未导入；未来引入相关包时必须重新评估，不把当前无可达调用结论沿用于新增功能。

此前自动审批拒绝追加依赖元数据发送。**2026-10-03 用户明确授权后，该阻塞已解除**，npm 官方审计与三个平台的 Go 官方漏洞详细扫描均实际执行完成。只发送审计所需依赖元数据，未发送源码或配置凭据；外部 OCR LLM 授权范围没有扩大。
