# MirrorDock 项目协作规则

开始工作前必须完整读取 [目录规范.md](目录规范.md)，再读取 [README.md](README.md)、[当前设计](docs/DESIGN.md) 和受影响模块。目录基线只在原文件维护，不复制正文。

本项目是一个 Go 后端配套 Vue Web 应用。Go 工具链固定 **1.27.1**，使用 `use-modern-go` 技能及其版本指南；新增 JSON 使用 `encoding/json/v2`。前端使用 Vue 3、TypeScript、Vite、npm，唯一输出为 `web/dist/`，由 `web/embed.go` 嵌入；凡需编译嵌入包的检查先构建 Web。

用户已确定的配置协议优先于目录基线的默认协议：`-f` / `--config` 选择 YAML，无参数读取工作目录的 `config.yaml`，相对路径以配置文件目录为基准；数据库为 `<path>/data.sqlite`。具体字段见 `configs/config.example.yaml`，不擅自恢复用户删除的配置字段。程序使用启动时区。源下载直接使用 `/<dest>/`，管理页面使用 `/admin/`，管理 API 使用 `/api/`。

同步调用系统 rsync、可选的 `dnf reposync`；GitHub Release 使用 `google/go-github` 和 Go 下载器，仅下载 assets。多来源与暂停共用累计运行预算。YAML 是配置真源，SQLite 保存运行事实；保留记录，页面手动清理终态日志。更新 YAML 保留注释、检查 revision 并原子写入，管理员密码、Token 和通知凭据不进入读取 API；完整代理 URL 回填为用户已确认的例外，见 README。凭证不进入审计、日志或运行快照。

下载性能落实 ADR-010：保留 HTTP/1.1 sendfile 路径、受限文件根、不可变发布代、文件流和有界资源；HTTPS 不虚称 sendfile。下载热路径不访问数据库、逐块写日志、整文件哈希或动态压缩。Linux 性能与实际发行版验收必须与本机合成测试区分。

实现完成后执行 `open-code-review` 自动审查，检查全部新增/修改的源码，修复影响正确性、安全性和性能的问题，再运行相关测试。前端改动运行 `npm --prefix web run format:check`、Web 构建和相关 E2E。审查报告、性能数据与未验证项集中记录在 `docs/ACCEPTANCE.md`；不要把工具失败或未执行的检查写成通过。未经用户授权不创建子代理，不发送外部消息，不推送或发布。

若外部 OCR LLM 调用被审批阻止，可使用 OCR 的本地 delegate preview/rule 工作流由当前 Codex 审查，明确记录覆盖清单、排除原因和外部调用未执行的事实；不要绕过审批发送源码。

项目命令和目录例外以 README 为入口。测试只能使用临时目录、隔离端口和可控上游，不启动示例中的完整发行版同步，不修改用户镜像和凭证。
