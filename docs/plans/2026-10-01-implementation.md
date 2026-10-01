# MirrorDock Implementation Plan

> 执行技能：executing-plans；阶段任务已完成，实际验证结果见 `docs/ACCEPTANCE.md`。

**Goal:** 实现可运行、可验证的 Go 1.27.1 + Vue 3 源同步和 HTTP/HTTPS 镜像服务。

**Architecture:** 模块化单体；YAML 保存配置，SQLite 保存任务和发布事实。后台执行器操作私有暂存目录，成功后原子发布；下载处理链直接读取受限发布根。

**Tech Stack:** Go 1.27.1、现代 JSON、modernc SQLite、yaml.v3、robfig cron、go-github、Vue 3 / TypeScript / Vite。

---

本次用户已授权继续实施，无需计划完成后再次确认。已为 OCR 工作区审查初始化本地 Git 仓库，没有创建远程、推送或自动提交。

### Task 1: 配置与持久化

- 创建 `internal/config/config.go`、`internal/config/config_test.go`、`internal/database/database.go`、`internal/database/database_test.go`；数据库初始化语句内置在 `internal/database`。
- 先验证缺省选项、srcN 数字顺序、保留路径、Token 脱敏和文件 revision 冲突，再实现 YAML 注释保留与 fsync/rename。
- 实现运行、尝试、事件、发布指针与管理员持久化；事务短小，恢复活动运行为 interrupted，暂停不丢预算。
- 验证：`go test ./internal/config ./internal/database`。

### Task 2: 受限下载与发布

- 创建 `internal/mirrors/`：不可变发布目录、受限 os.Root、根目录列表、HEAD/Range/ETag、上一代保留、私有暂存与持久恢复。
- 用临时文件先测试 Range、越界链接、私有文件和发布并发，再实现传输。
- Linux reflink 优先，不支持时有界普通复制；不复制整个文件进内存。HTTP 文件路径透传原始 ResponseWriter。
- 验证：`go test ./internal/mirrors`，添加可重复文件服务 benchmark。

### Task 3: 执行器、状态机与计划

- 创建 `internal/sync/` 和 `internal/runs/`：rsync 参数、进程组停止、进度解析、DNF 私有配置、go-github 下载和 Range 续传。
- 测试源切换、累计超时、暂停继续、局部错误禁止切换、重启恢复；无真实上游时用合成可控命令/HTTP 服务。
- 创建 cron 调度；同源活动/暂停任务占用目标，运行快照脱敏。
- 验证：`go test ./internal/sync ./internal/runs`。

### Task 4: API 与实际 Vue 页面

- 创建 `internal/app/`、`cmd/mirrordock/main.go`、`web/embed.go`。
- 管理员账号使用 YAML 的 auth.username/auth.password，启动时生成内存 bcrypt 校验值并使用 Cookie 会话鉴权；写操作检查同源/CSRF。公共下载不经过管理鉴权。
- 创建 `web/src/app/`、`layouts/`、`features/`、`components/`、`styles/` 和 Logo，落实参考图的侧栏、筛选、时间线与成功/失败记录。
- 配置编辑、立即同步、暂停继续、详情日志、清理日志、SSE、计划和 TLS 字段全部接实际 API。
- 验证：`npm --prefix web run check`、`npm --prefix web run build`、`go test ./...`、浏览器端到端测试。

### Task 5: 构建、部署、审查与验收

- 写 README、Dockerfile、systemd 定义和忽略规则，所有编译入口显式先生成 Web。依据目录规范，未连接 CI 平台时只记录实际执行的本地等价检查，不虚构远程流水线配置。
- 执行 `go vet ./...`、`go test -race ./...`、Linux 交叉构建、干净副本安装/构建/最小启动。
- 运行 OCR 工作区自动审查，逐条核实并修复，然后重新验证受影响部分。
- 更新 `docs/IMPLEMENTATION.md`、`docs/ACCEPTANCE.md` 与设计的实施状态；详细区分已实现、已实测与 Linux/真实发行版/网络满载待验收项。

### 完成状态

Task 1–5 的代码、页面、构建与本地验收已完成。Go 静态检查/竞态、现代 rsync 实际协议、TLS/HTTP2、浏览器端到端、干净副本最小启动与 Linux 双架构编译通过。OCR 外部 LLM 调用被自动审批阻止，已完成本地 delegate 的全部 50 个生产文件审查及相关修复。Linux 实际 sendfile/物理吞吐、完整 EL reposync 和容器运行留作目标环境验收，详见验收记录。
