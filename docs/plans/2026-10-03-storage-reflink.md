# 镜像存储与 reflink 实施计划

目标：在已开启 reflink 的 XFS 上保留不可变发布与 24 小时旧包保护，阻止静默全量复制，清理终态暂存，在系统设置展示实际探测结果。

1. `internal/mirrors`：实际 published → work 目录探测；克隆失败返回原因；macOS 使用原生 APFS 克隆以保持开发验证真实；统计文件系统总量/可用量，两个 worker 共用容量准入与运行中低空间保护。
2. `internal/mirrors`：准备时保存有界元数据指纹，只有确认工作树未变化才跳过发布；不读取包正文计算哈希。保留原发布事务与下载引用生命周期。
3. `internal/runs`：启动、周期、同步前后回收；终态持久化且执行器退出后清理 work，保留暂停/中断、记录和日志。容量不足保持排队，不消耗执行预算。
4. `internal/sync`：移除 rsync delay-updates，保留 partial-dir 和单文件原子替换。
5. `internal/app`、`web/src/features/settings`：环境检查显示 reflink 支持/不支持/探测失败、镜像盘容量与安全余量；刷新重新探测。
6. 临时目录回归覆盖克隆失败无复制、克隆隔离、无变化/删除/正文变化、低空间/两个 worker、终态与可恢复状态清理、旧代活动引用；前端格式、构建、相关 E2E。
7. 执行 OCR 审查，修复有效问题后重跑相关测试，更新 README/DESIGN/IMPLEMENTATION 与 ACCEPTANCE。生产 XFS 多 TB 峰值不以本机 fixture 冒充验收。

容量策略使用内置安全余量：文件系统总量的 1% 与 1 GiB 取大；准入另为每个活动 worker 预留 512 MiB。运行中周期检查，低于安全余量停止任务并保留旧发布。这是提前停止机制，不是文件系统硬配额；外部写入与检查间隔内突增仍可能触发 ENOSPC。首次同步仍需要整套镜像实际容量。
