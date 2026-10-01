# 配置账号与移除顶部栏实施计划

> 按用户已确定的 YAML 账号协议在当前工作区实施，由当前 Codex 完成修改、审查和验证。

**Goal:** 使用 `auth.username`、`auth.password` 登录，删除初始化码流程及截图圈出的顶部栏。

**Architecture:** YAML 是账号唯一来源，账号在启动时加载，密码生成内存中的 bcrypt 校验值，修改账号后重启生效。配置读取隐藏密码，页面保存其他字段保留完整 auth；登录状态只为已认证的会话返回用户名。Vue 登录页使用用户名与密码，侧栏显示实际账号；移除整条顶部栏，手机仍能打开侧栏。

**Tech Stack:** Go 1.27.1、encoding/json/v2、yaml.v3、bcrypt、Vue 3、TypeScript、Vite、Playwright。

---

1. 修改 `internal/config/config.go`：增加 Auth 与非空/长度校验；密码不参与 JSON 编码；Clone 显式保留 Auth；View 脱敏并检测账号是否需要重启；Save 从当前配置保留 Auth。扩展 `internal/config/config_test.go`，覆盖脱敏、评论、重载、回写及错误不包含凭证。
2. 修改 `internal/app/auth.go`、`internal/app/app.go`、`cmd/mirrordock/main.go`：删除 setup code、setup API、SQLite 密码读取和终端初始化提示；从启动配置生成 bcrypt 校验值；登录校验用户名及密码，保留限流、Cookie 和同源检查。更新 app/runs 临时配置测试，覆盖错误账号、旧密码记录、重载后重启和会话失效。
3. 修改 `web/src/app/App.vue`、`state.ts`、`types.ts`、`web/src/layouts/Shell.vue`、`web/src/styles/global.css`：增加用户名登录、显示实际账号、删除顶部栏及其样式，在手机保留独立导航按钮。更新 `web/e2e/mirrordock.spec.ts`，验证登录、密码保存和接口脱敏、五页顶部栏移除及手机导航。
4. 更新 `configs/config.example.yaml`、README、设计与实现说明。执行本地 OCR delegate 的 preview/rule 并逐文件审查，修复确认的正确性、安全性和性能问题，再运行 Web 构建、Go vet/竞态测试、二进制构建和隔离浏览器 E2E。将实际结果与未执行项写入 `docs/ACCEPTANCE.md`；测试与审查均不复制用户配置、凭证或镜像。

验证命令从仓库根执行，先构建 Web：`npm --prefix web run build`；`GOCACHE="$PWD/.cache/go-build" go vet ./...`；`GOCACHE="$PWD/.cache/go-build" MIRRORDOCK_TEST_RSYNC="$PWD/.cache/rsync/rsync-3.5.1/rsync" go test -race ./...`；构建测试二进制后运行 `npm --prefix web run test:e2e`，用临时配置、目录、端口和可控 rsync 上游。

实施与本地审查已完成，Web 构建、Go vet/竞态测试、本机与 Linux amd64 构建、隔离浏览器 E2E 均通过。最终结果与未验证范围见 [验收记录](../ACCEPTANCE.md)。
