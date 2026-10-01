# 通知设置实施计划

目标：在现有系统设置中实现五渠道通知、加密配置、独立持久队列与记录。

架构：YAML 保持配置真源，SQLite 保存激活边界、通知快照和发送状态。通知 worker 每 3 秒扫描已持久化终态，不在同步任务或下载路径发送 HTTP。沿用 Cookie、同源写入验证、revision 和原子 YAML 写入。现有项目无加密密钥管理，新增数据目录 0600 密钥文件，AES-GCM 密文绑定渠道及字段。

技术栈：Go 1.27.1 / encoding/json/v2 / SQLite / Vue 3 / TypeScript。

1. 扩展 internal/config：通知配置类型、保留敏感字段的专用保存、禁止通用配置 API 覆盖通知；新增 internal/notifications 的加密和渠道校验。
2. 实现发送适配：企业微信、钉钉签名、Telegram、Bark 路径前缀及 /push、通用 JSON；固定错误消息、10 秒超时、禁重定向、64 KiB 上限。
3. 实现 SQLite 队列与状态恢复：渠道/任务唯一约束、激活任务 ID 边界、资格过滤、三次尝试与 30/60 秒退避、快照及最新 30 条记录。
4. internal/app 装配 worker 和受保护接口；登录后同步通知时区，嵌入 time/tzdata；保持现有调度时区。
5. 新增 web/src/features/settings/Notifications.vue，横向响应式卡片、可访问帮助、独立操作反馈、5 秒记录轮询；扩展 Settings 与 App 会话流程。
6. 本地接收端与 Go/API/浏览器测试覆盖保存、安全、队列、业务响应、恢复和时间；执行 npm build、go vet、go test -race、Linux 交叉编译。
7. 执行 open-code-review，修正问题，再运行相关测试；在 docs/ACCEPTANCE.md 如实记录检查和未验证项，更新 README 和设计。

不提交、不推送，不向真实渠道发送；保留开始时已有工作区改动。
