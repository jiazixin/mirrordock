# 实现与维护入口

当前架构与约束见 [DESIGN.md](DESIGN.md)，启动/配置/备份/测试命令见 [README.md](../README.md)，可追溯验收见 [ACCEPTANCE.md](ACCEPTANCE.md)。目录基线只在 [目录规范.md](../目录规范.md) 维护。

## 数据与接口

数据库由 `internal/database` 启动建表/索引，1.0 初始 schema（无发布前兼容迁移）。运行数据以单条 JSON 聚合 attempts/events/脱敏快照；发布指针独立存储。历史保留，日志清理不删除事实。WAL/FULL 单写连接，分析走有界只读快照。`runs(source_id,id DESC)` 支持每源最新状态，不能用全局最近 50/100 条代替。

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

来源 URL 与 GPG 公钥 URL 禁止用户信息、查询参数及片段。`internal/runs/snapshot.go` 负责新快照的防御性脱敏。首次上线使用新的 state；发布前的历史地址迁移代码已删除。run_results 的自增 sequence 表示结果插入顺序，用于通知有界扫描，id 唯一约束继续保留首个不可恢复终态事实（中断不写结果队列）。

## 源码定位

- 配置及路径：`internal/config/config.go`；所有 dest 同时检查逻辑路径和解析后目录重叠。
- 任务与重试：`internal/runs/runs.go`；时间范围：`internal/app/location.go`；分析：`internal/runs/analytics.go`。
- Rsync/DNF 命令：`internal/sync/runner.go`；子进程和进度：`process.go`；GitHub：`github.go`。
- 文件与发布：`internal/mirrors/{mirrors,http,validate,summary}.go`；克隆/容量/无变化复用：`clone_*.go`、`space_*.go`、`storage.go`、`fingerprint.go`；公开目录：`listing.go` 与 listing HTML/CSS/JS/icons。
- 文件句柄：`internal/mirrors/descriptors.go` 与 `internal/app/connections.go` 分配同一进程预算；代根按需打开，不按历史代常驻。文件流与请求引用保持至请求结束。
- 凭据/发送队列：`internal/notifications`；独立程序日志：`internal/observability`；认证/服务生命周期：`internal/app`。
- Vue 请求与会话：`web/src/app`；业务页面：`features`；共享控件：`components`；所有页面共享格式化规则。

## 实施边界

GitHub 原始日志有资产结果，当前没有每资产独立数据库检索页。Reposync 未提供可靠包数/接收量，不推算百分比或统计。公开目录不分页，最多列出 1000 项；符号链接不回显目标。大小摘要在发布/恢复时生成，文件下载不扫描目录树。

程序不自动轮转日志、不删除历史、不过滤任意管理员 Webhook 为公共互联网地址、不自动提权；这些是当前运维协议。镜像发布代需保持不可变，数据与镜像根只能由受信任服务身份维护。Linux 性能、真实 EL/DNF、外部通知可达性、容器及断电恢复验收均以当次 ACCEPTANCE 证据为准。

`/api/system/checks` 的 `reflink.status` 为 supported/unsupported/error，`detail` 记录实测结果；`storage` 给出 work 文件系统 total/available/reserve/required（下一任务准入）、workers、当前路由 previous 数与数字 work 目录数，读取失败通过 error 返回。目录数不表示物理空间或所有已删除配置源的留存文件。生产同步要求可用 reflink；容量保护为提前停止策略，首次全量及增量峰值仍需实际容量规划。

已完成计划归档于 `docs/archive/plans/`，历史版本和方案只作追溯，不能当作当前实现。本次完整工作区（含已有改动）的审查与回归记录集中在 ACCEPTANCE，不再追加按会话堆叠的历史段落。
