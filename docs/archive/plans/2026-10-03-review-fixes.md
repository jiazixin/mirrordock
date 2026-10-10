> 历史实施记录：已归档，仅用于追溯。当前工具链、协议与实现以 README、DESIGN 和 ACCEPTANCE 为准。

# 全项目审查问题修复计划

目标：修复 ACCEPTANCE 中 R1–R7，保留现有配置字段、发布隔离和下载流路径，补齐永久回归测试。

技术栈：Go 1.27.1、encoding/json/v2、SQLite、Vue/Vite/npm、Playwright。直接在当前工作区实施，保留已有改动，不提交、推送或调用子代理。

1. `internal/runs/runs.go`：恢复前与 worker 出队时检查 working；恢复先保存后排队，暂停/取消保存失败保持与执行器一致的状态。测试写库失败、停止窗口和恢复保存失败。
2. `internal/mirrors/{mirrors,http}.go`：发布路由保存受限代路径和引用，不常驻打开全部根；请求先获取有界 FD 配额，逐代按需打开根，打开文件后关闭根。测试低 FD 多代发布、旧包回退、活动引用、限流及越界。
3. `internal/config/config.go`：专门的来源 URL 校验拒绝 userinfo/query/fragment，不改变通用 HTTPURL 的签名下载能力。`internal/runs`/`internal/database`：新快照防御性脱敏、启动时分页脱敏历史 runs/run_results 的来源 URL、尝试和事件。测试配置拒绝、签名 URL 接受、历史两表脱敏及读取。
4. `internal/sync/github.go`：私有同步标记更新后恢复父目录 mtime；无变化正文复用当前代时单独持久化更新摘要时间，保持正文修改身份检查。测试连续资产复用以及实际资产变化仍发布。
5. `internal/mirrors` 克隆：按 dev/inode 保留工作树内部硬链接组，仅首个正文 reflink，其余链接到 work 克隆；不链接到 published。测试 rsync 无变化与写入隔离。
6. `internal/notifications/message.go`：耗时用 ElapsedSeconds；缺失累计值显示未记录；复用为成功子集，不重复扣减。测试暂停耗时、旧记录、部分失败。
7. 先构建 Web；执行相关回归和全套 Go race、vet、构建、Web 格式及全部 E2E。执行 open-code-review；外部入口不可用则使用允许的本地 delegate preview/rule，逐项审查全部新增/修改源码并复验。更新 DESIGN/README/IMPLEMENTATION 的相关行为和 ACCEPTANCE 的实际证据及未验证项。

完成标准：七项缺陷永久测试通过，现有全套测试通过，没有遗留已确认的正确性、安全性或性能问题；Linux 实机、真实发行版与断电检查不能以本机结果替代。
