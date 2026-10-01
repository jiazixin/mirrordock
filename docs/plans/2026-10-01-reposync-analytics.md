# Reposync 分析实施计划

**Goal:** 在侧栏新增“Reposync分析”，展示可从运行事实可靠汇总的次数、成功率、耗时、失败原因和重试指标。

**Architecture:** 复用现有 Analytics.vue、SVG 图表与 SQLite 只读聚合模块，新增 reposync 类型和路由。Reposync 隐藏处理量趋势、包数、复用量、传输字节和备用源成功指标；不改执行器、配置或数据库结构。

**Tech Stack:** Go 1.27.1、encoding/json/v2、SQLite、Vue 3、TypeScript、Vite、Playwright。

## 实施与验证

1. 扩展 `internal/runs/analytics_test.go`，用临时 SQLite 混合三种类型、终态/活动/取消、重试和窗口边界，验证 reposync 类型隔离、耗时、错误、日汇总和源筛选。更新 `internal/app/analytics_test.go`，验证鉴权、未知类型拒绝及 reposync 请求成功。
2. 在 `internal/app/analytics.go` 和 `internal/runs/analytics.go` 的类型校验中允许 reposync，保留 SQL 参数化、5 秒超时、只读一致事务和聚合边界。
3. 更新 `web/src/app/types.ts`、`web/src/app/router.ts`、`web/src/layouts/Shell.vue`。共享页面根据类型选择标题和指标；Reposync 第四张统计卡展示重试任务，趋势仅次数/耗时，执行卡展示尝试/失败尝试/重试，排行展示重试次数。
4. 扩展 `web/e2e/analytics.spec.ts`：真实空数据 API、导航、筛选隔离、指标隐藏、跨类型切换/重置、图表切换和窄屏。合成 reposync 响应刻意包含非零传输字段，验证页面不会误展示未知统计。
5. 更新 README、当前设计和实现说明。执行 OCR 本地 delegate preview/rule，由当前 Codex 覆盖所有变更源码，补查默认排除测试与文档；修复确认的问题。
6. 审查后 `npm --prefix web run build`，再执行 `GOCACHE="$PWD/.cache/go-build" go test -race ./internal/runs ./internal/app -run Analytics`、`go vet ./internal/runs ./internal/app` 和 Go 嵌入构建；临时二进制运行相关 Playwright 测试。记录实际结果、审查覆盖和未验证项到 ACCEPTANCE。

沿用用户已确认方案，不创建子代理或新会话；保留上轮 Release分析改名。所有测试使用临时数据与隔离端口，不触碰用户镜像和凭证，不执行真实发行版同步。
