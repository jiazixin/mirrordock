# Go 代码规范

基线版本：1.0.2。适用范围：Go 后端服务、命令行工具与库。先读 [代码规范.md](代码规范.md)；`cmd/`、`internal/`、`testdata/`、配置文件位置与版本元数据见 [目录规范.md](目录规范.md)，本文不重复。第 10 节命令行入口契约与附录只在新建或修改 `cmd/` 时阅读。

## 1. 工具链

- 可执行程序的 `go.mod` 中 `go` 指令写到补丁版本（如 `go 1.27.3`），同一时期的各仓库使用同一补丁版本，不另写不同的 `toolchain` 指令；对外发布的库按实际需要的最低版本填写。CI 从 `go.mod` 读取 Go 版本。
- 运行环境提供 `use-modern-go` 技能时，编写或修改任何 Go 代码（含测试）前必须按该技能的流程获取适用规则，并按 `go.mod` 版本采用现代写法；技能不能代替下列检查。
- CI 必须依次运行以下检查，任何一项失败即失败。检查范围只取 Git 跟踪的源码，不扫描 `.cache/`、`test-results/` 等目录：

```sh
go mod verify
go mod tidy -diff
test -z "$(gofmt -l $(git ls-files '*.go'))"
test -z "$(go run golang.org/x/tools/cmd/goimports@<固定版本> -local <模块路径> -l $(git ls-files '*.go'))"
go vet ./...                                   # 每个发布平台各运行一次（GOOS/GOARCH）
go vet -tags integration,scale ./...
go run honnef.co/go/tools/cmd/staticcheck@<固定版本> ./...
CGO_ENABLED=1 go test -race -count=1 ./...     # -race 需要 cgo 与 C 编译器
go run golang.org/x/vuln/cmd/govulncheck@<固定版本> ./...
```

  工具版本在 CI 配置中固定，不用 `@latest`。既有仓库首次接入 goimports 或 staticcheck 时，先单独提交一次全量对齐，再把检查加入 CI。
- 仓库含 `web/` 时，`go.mod` 必须写 `ignore ./web/node_modules`（多应用布局写对应路径），否则 `./...` 会把 npm 依赖中的 `.go` 文件当作本模块的包参与检查与 `go mod tidy`。
- staticcheck 只允许收紧、不允许放宽：仓库根的 `staticcheck.conf` 固定为 `checks = ["inherit", "ST1000", "ST1020", "ST1021", "ST1022"]`，用于检查包注释与导出标识符的文档注释。
- 禁止 `//nolint`。staticcheck 的单处抑制写 `//lint:ignore <检查> <原因>`。
- 依赖选择：路由用标准库 `net/http`；日志用 `log/slog`；JSON 用 `encoding/json/v2`；YAML 用 `go.yaml.in/yaml/v3`（禁止新引入已归档的 `gopkg.in/yaml.v3`）；SQLite 用 `modernc.org/sqlite`；不引入 testify、mock 生成器、ORM 和第三方 Web 框架。JSON 以包为单位：已使用 `encoding/json` v1 的包继续使用 v1，新包使用 v2，不在同一个包内混用。

## 2. 格式、命名与注释

- import 分三组，组间空一行：标准库、第三方、本模块（与 `goimports -local <模块路径>` 一致）。
- 顶层声明之间空一行；不把多条语句挤在一行的函数体中。行宽以 120 列为宜，超长的字面量拆行。
- Go 文件名小写，单词间用下划线（`directory_scan.go`）。文件名最后一段不得是 GOOS / GOARCH 名称（`android`、`ios`、`js`、`wasm`、`windows`、`linux`、`darwin`、`amd64`、`arm64` 等），除非它确实是平台文件；平台文件不再重复写等价的 `//go:build`。
- 包名为小写单词，与目录名一致，不用下划线和复数，不叫 `util`、`common`、`helpers`。嵌入 Web 资源的 `web/embed.go` 为 `package web`，使用 `//go:embed all:dist` 并以 `fs.Sub(files, "dist")` 对外提供；禁止 `//go:embed dist`，它会排除以 `_`、`.` 开头的构建文件。
- 错误变量命名为 `err`；接收者名 1–2 个字母，同一类型的方法保持一致；禁止遮蔽内建名（`error`、`recover`、`len`、`new`、`min`、`max`）和导入的包名（如把接收者命名为 `config`）。
- 短名只用于作用域在十行左右以内的局部变量和惯用场景（`ctx`、`err`、`w`、`r`、`i`、`n`、`ok`、`tx`、`tc`、`t`、`b`、`fn`、循环变量）；包级标识符与导出字段使用完整单词。
- HTTP handler 参数统一为 `w http.ResponseWriter, r *http.Request`。
- 文件权限写 `0o600`、`0o750` 形式。
- 每个包有且只有一份包注释：普通包为 `// Package <名称> ...`，放在与包同名的文件或 `doc.go` 中；`main` 包为 `// Command <程序名> <动词短语>.`。导出的标识符必须有以其名称开头的文档注释。
- 接口在使用方定义，只包含使用方需要的方法；不为只有一个实现、也不需要替换的类型预先定义接口。
- 函数参数超过三个（不计 `ctx` 与接收者）或有多个同类型布尔参数时，改为参数结构体。

## 3. 错误

- 无格式参数的错误用 `errors.New`，不用 `fmt.Errorf("...")`。
- 包装格式为 `fmt.Errorf("<动作> <对象>: %w", err)`：小写英文开头、不带句号、只用半角冒号，例如 `fmt.Errorf("open library %q: %w", name, err)`。在 `fmt.Errorf` 中禁止用 `%s` / `%v` 格式化错误值，那会切断错误链；输出到终端或日志时不受此限。
- 跨包边界返回错误时加上能定位问题的上下文；同一函数内上层已包含相同上下文时直接返回，不重复包装。
- 判断错误用 `errors.Is`，提取错误类型用 `errors.AsType`（`go.mod` 低于 1.26 时用 `errors.As`）；禁止 `err == ErrX`、`err != io.EOF` 等直接比较，禁止 `os.IsNotExist` 等旧函数，改用 `errors.Is(err, fs.ErrNotExist)`。
- 领域包返回哨兵错误（`ErrNotFound = errors.New("not found")`）或自己的错误类型；错误文本为英文小写短语，不放中文提示。
- 接口错误码只在 HTTP 层出现：`internal/app`（或项目的 HTTP 装配包）中唯一的 `writeError` 用 `errors.Is` / `errors.AsType` 把领域错误映射为状态码与 snake_case 错误码，见第 6 节。领域包不依赖 HTTP 层，不返回状态码。
- 需要把详情返回给客户端的领域错误（如校验错误的字段路径）定义为领域包内的错误类型，实现 `Error()` 与 `Unwrap()`，由 `writeError` 转换为 `details`。
- 用户可见的中文提示不在 Go 中生成，由客户端按错误码映射。
- panic 只用于程序员错误和不可能发生的状态；不在 `crypto/rand.Read` 失败分支里 panic，生成随机令牌用 `rand.Text()`。HTTP 处理中需要中止响应时可以 `panic(http.ErrAbortHandler)`。

## 4. 日志

- 使用 `log/slog`。`cmd` 根据配置创建 `*slog.Logger`，通过构造参数传给需要日志的组件；组件不调用全局 `slog.Info` 等函数，不调用 `slog.SetDefault`。组件可用 `logger.With("component", "<名称>")` 派生。
- 消息为 snake_case 英文事件名（`scan_failed`），字段键为 snake_case。错误写 `"error", err`，不调用 `err.Error()`；标识符字段为 `<实体>_id`；耗时为整数 `duration_ms`。
- 服务进程默认用 JSON handler 输出到 stdout；命令行工具的日志与诊断写 stderr，stdout 只留给命令结果。级别与格式来自配置。
- 请求日志带 `request_id`，与响应头 `X-Request-ID` 相同。
- `http.Server.ErrorLog` 用 `slog.NewLogLogger(handler, slog.LevelError)` 接入同一个日志。

## 5. context、并发与生命周期

- 网络、数据库、外部进程调用，以及可能长时间运行的文件操作（如目录扫描、大文件复制），第一个参数为 `ctx context.Context`。进程启动时一次性读取本地配置文件不在此列（第 7 节的 `Load`）。不新增“无 ctx 的 `Xxx` + 带 ctx 的 `XxxContext`”成对 API，不在库代码中用 `context.Background()` 代替调用方的 ctx。关闭阶段需要脱离取消时用 `context.WithoutCancel(ctx)` 加超时。
- 测试辅助函数可接收 `*testing.T` 并从 `t.Context()` 获取上下文，无须再接收一个 ctx 参数；网络、外部进程等需要超时的操作仍从该上下文派生明确的操作超时。`t.Context()` 不自带 deadline，测试总超时不能代替操作超时。
- 不把 `context.Context` 存进结构体字段，更不导出。
- 有后台工作的组件：`New(...)` 只做构造与校验，不启动 goroutine；`Run(ctx) error` 启动后台工作，阻塞到 `ctx` 结束且所有 goroutine 退出后返回。应用装配确需分步启动（例如先监听端口再启动后台任务）时使用 `Start(ctx) error` 与 `Shutdown(ctx) error`，并在包注释中说明顺序。
- 后台 goroutine 用 `sync.WaitGroup.Go` 启动并等待；需要“任一失败即取消全部”时组合 `context.WithCancelCause`，不为此引入额外依赖。每个 goroutine 都有退出路径。
- 共享状态的保护方式在类型注释中写明（哪个锁保护哪些字段）。

## 6. HTTP

- 路由使用 `http.ServeMux` 的方法与路径模式（`mux.HandleFunc("GET /api/items/{id}", ...)`），路径参数用 `r.PathValue`；不手写 `switch r.URL.Path` 分发。`/api/` 下不注册以 `/` 结尾的模式，避免 ServeMux 的 307 重定向。
- 用一个包装 handler 把 ServeMux 生成的 404 / 405 文本响应改写为 [代码规范.md](代码规范.md) 第 5.3 节的 JSON 错误（`not_found`、`method_not_allowed`）。
- 统一中间件（签名 `func(http.Handler) http.Handler`）：
  - panic 恢复：记录日志并返回 500 `internal`；遇到 `http.ErrAbortHandler` 重新 panic；响应头已写出时只记录日志，不再写错误体。
  - 请求 ID：入站 `X-Request-ID` 只在匹配 `^[A-Za-z0-9-]{1,64}$` 时沿用，否则重新生成；写入响应头与请求日志。
  - 安全响应头：`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、`Content-Security-Policy: default-src 'self'; frame-ancestors 'none'; object-src 'none'; base-uri 'none'`（项目确需放宽时在 README 写明）；`/api/` 响应带 `Cache-Control: no-store`。
  - 跨站防护：浏览器会话使用 `http.NewCrossOriginProtection()`；鉴权见 [代码规范.md](代码规范.md) 第 5.4 节。
- 状态码一律使用 `http.StatusXxx` 常量，不写数字字面量。
- 请求体用 `http.MaxBytesReader` 限制大小；JSON 解码使用 `json.UnmarshalRead(r.Body, &v, json.RejectUnknownMembers(true))`（仍使用 v1 的包用 `Decoder.DisallowUnknownFields()` 并确认之后读到 EOF）。解码错误命中 `errors.AsType[*http.MaxBytesError]` 时返回 413 `payload_too_large`；`Content-Type` 不是 `application/json` 时返回 415 `unsupported_media_type`。
- 查询参数与路径参数解析失败返回 400 `invalid_argument`，不静默回退为零值；参数多于三个时先解析成一个请求结构体再传递。
- 错误响应只通过唯一的 `writeError(w, r, err)` 输出：已知的领域错误映射为对应状态码与错误码；未知错误返回 500 `internal`，记录完整错误，响应中不包含 `err.Error()`。
- `http.Server` 必须设置 `ReadHeaderTimeout`、`ReadTimeout`、`WriteTimeout`、`IdleTimeout` 与 `MaxHeaderBytes`。长时间流式响应在 handler 中用 `http.NewResponseController(w).SetWriteDeadline` 按需延长，不全局关闭写超时。
- 出站请求使用自建的 `http.Client`，不用 `http.DefaultClient`。普通 API 调用设置 `Timeout`；流式下载不设整体 `Timeout`，改用 `Transport` 的连接与响应头超时加 ctx 截止时间。携带鉴权信息的客户端设置 `CheckRedirect` 返回 `http.ErrUseLastResponse`。响应体必须关闭，读取有大小上限。
- 访问用户可控根目录下的文件使用 `os.OpenRoot` / `os.OpenInRoot`；校验相对路径用 `filepath.IsLocal`。

## 7. JSON、YAML 与配置

- JSON 字段标签为 snake_case；可省略字段使用 `omitzero`。JSON 结构体不含 `time.Duration` 字段，改用整数 `_ms` 字段。
- 接口与存储中的时间使用项目内统一的时间类型（如 `internal/timefmt.Time`），以 UTC、固定毫秒精度的格式 `2006-01-02T15:04:05.000Z` 实现 `MarshalText`（先调用 `t.UTC()` 再 `Format`，因为字面量 `Z` 不做时区转换）与 `UnmarshalText`；不直接序列化 `time.Time`，因为默认输出的小数位数不固定。
- 配置结构为值类型，YAML 标签为 snake_case；解码时拒绝未知字段与多文档。运行期可编辑的配置（如管理端修改后热加载）记录为项目例外。
- 配置加载入口固定为：

```go
// Source describes where configuration comes from.
type Source struct {
	Path      string                      // selected file
	Explicit  bool                        // chosen by --config or CONFIG_FILE
	WorkDir   string                      // absolute base for relative Path and built-in defaults
	LookupEnv func(string) (string, bool) // os.LookupEnv in production
}

func Load(src Source) (Config, error)
func (c Config) Validate() error
```

  `Load` 只读取本地配置文件，且在建立信号 ctx 之前运行，因此不接收 `ctx`（见第 5 节）。`Explicit` 为真时文件必须存在；为假时文件缺失可回退到内置默认值，但必填项缺失仍报错。`WorkDir` 同时用于解析相对 `Path` 和内置默认路径；即使文件存在，其中省略的路径字段也以 `WorkDir` 为基准。文件中显式填写的相对路径以该文件所在目录为基准，见目录规范第 7.1 节。`Validate` 只校验、不修改，规范化（去空白、转小写、补默认值）在解码阶段完成。
- 校验错误使用带字段路径的错误类型，消息为英文：`server.port: must be between 1 and 65535`。
- 字段级环境变量覆盖的变量名为：程序名转大写、`-` 换成 `_`，作为前缀；层级之间用 `__` 连接，键名内部保留单个 `_`（如 `HARBOR_CONSOLE_SERVER__LISTEN_ADDR`）。协议写入 README 的“配置约定”，见目录规范第 7.1 节。

## 8. 数据库（SQLite）

- DSN 使用 `_pragma=` 写法设置 `foreign_keys(1)`、`journal_mode(WAL)`、`busy_timeout(5000)` 等。写连接 `SetMaxOpenConns(1)` 并使用 `_txlock=immediate`；需要并发读时另开只读连接池，DSN 为 `file:<路径>?mode=ro&_pragma=query_only(1)`（不带 `file:` 前缀时 `mode=ro` 不生效）。
- 存储类型不嵌入 `*sql.DB`，不导出连接字段；对外只暴露业务方法。
- 所有调用使用带 ctx 的方法（`ExecContext`、`QueryContext`、`QueryRowContext`、`BeginTx`）。
- 写事务统一经由一个封装执行：`func (s *Store) Write(ctx context.Context, fn func(tx *sql.Tx) error) error`，封装内负责 `Rollback` / `Commit`。持有写事务期间不做文件、网络或外部进程 I/O。
- 遍历结果集必须检查 `rows.Err()` 并关闭 `rows`；重复的扫描逻辑提取为泛型辅助函数。
- SQL 只用参数占位符；表名、列名等无法参数化的部分只能来自代码中的常量白名单。
- Schema 只有一个来源：嵌入的 `.sql` 文件或 Go 常量。使用目录规范的根 `migrations/` 时，以 `migrations/embed.go`（`package migrations`）嵌入。版本记录在 `PRAGMA user_version` 或迁移表中；版本不一致时“拒绝打开”还是“执行迁移”由项目在开发文档中规定，已发布项目必须提供迁移。
- 密码使用 argon2id 或 bcrypt 存储；令牌只存 SHA-256 摘要，校验时用 `subtle.ConstantTimeCompare` 比较摘要。

## 9. 测试

- 只用标准库 `testing`，断言失败输出 `got ..., want ...`。表驱动测试用 `for _, tc := range []struct{...}{...}` 加 `t.Run(tc.name, ...)`。
- 使用 `t.Context()`、`t.TempDir()`、`t.Setenv()`、`t.Chdir()`、`t.Cleanup()`；辅助函数第一行调用 `t.Helper()`。
- 可以并行的测试调用 `t.Parallel()`；使用 `t.Setenv`、`t.Chdir` 或共享全局状态的测试不并行。
- 时间相关逻辑：
  - 只涉及内存中定时器、超时、轮询的逻辑用 `testing/synctest`，在其中用 `time.Sleep` 推进虚拟时间是正确用法；
  - 涉及真实网络、文件或外部进程时，注入 `now func() time.Time` 并用轮询断言等待；
  - 其他情况禁止用 `time.Sleep` 等待异步结果。
- 替身：单个函数依赖用未导出的函数字段（如 `now`），多方法依赖在使用方定义小接口并写假实现；不导出仅供测试替换的字段，不在 `_test.go` 中给生产类型添加方法。外部命令用写入 `t.TempDir()` 的脚本并通过 `PATH` 注入。
- 构建标签只用 `integration`（需要外部工具、真实构建或较长时间）与 `scale`（规模测试）；运行命令写入开发文档。
- 解析器、协议解码等处理不可信输入的函数提供 `Fuzz` 测试，语料放 `testdata/fuzz/`。
- 基准测试使用 `b.Loop()`。
- HTTP 处理使用 `httptest`；接口契约测试使用 [代码规范.md](代码规范.md) 第 5.1 节规定的样例。

## 10. 命令行入口契约

本节是固定的目标协议。新程序直接按此实现；已有程序与本节的差异属于存量差异，按 [代码规范.md](代码规范.md) 第 2 节处理，整体对齐作为单独任务进行。

### 10.1 程序类型

- **服务**：长期运行、通过配置文件启动的进程（Web 服务、同步守护进程）。适用本节全部规则。
- **命令行工具**：执行一次任务后退出的程序，可以有子命令、自己的选项和位置参数。适用本节规则，并按第 10.8 节调整。
- `cmd/` 下仅供本程序内部启动的辅助进程入口（如通过参数进入的 rsync 协议模式）可在 `main()` 调用 `run` 前分发，必须注释原因。

### 10.2 文件

| 文件 | 内容 |
| --- | --- |
| `main.go` | `// Command` 包注释、退出码常量 `exitOK = 0` / `exitFailure = 1` / `exitUsage = 2`、`main()`、`run()` |
| `cli.go` | `usage` 常量、`options`、`parseArgs`、`configPath` |
| `log.go` | `newLogger`：按配置创建日志与其关闭函数（服务需要时） |
| `version.go` | 版本读取，逐字使用附录 B，只替换注释中的程序名 |
| `cli_test.go`、`main_test.go` | 参数、退出码与输出测试 |
| `version_test.go`、`version_build_test.go` | 逐字使用附录 C |

`main()` 只有一行 `os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))`（第 10.1 节的辅助进程分发除外）。`run` 不调用 `os.Exit`、不读 `os.Args`，输出只写传入的 `stdout` / `stderr`。

### 10.3 帮助文本

`usage` 是一个常量字符串，结构固定如下；行宽不超过 80 列，描述列从第 24 列开始，选项名过长时另起一行并缩进到同一列：

```text
<程序名> - <一句话说明，小写开头，不加句号>

Usage:
  <程序名> [options]

Options:
  -c, --config <file>  Configuration file (default: configs/config.local.yaml)
  -h, --help           Print this help and exit
      --version        Print version information and exit

Environment:
  CONFIG_FILE          Configuration file, used when --config is not given

<可选：项目说明段落，例如配置优先级、相对路径基准、外部依赖>
```

- 帮助、版本、错误、日志一律英文。
- 有字段级环境变量覆盖时，在 `Environment:` 中写出变量名格式并举例。
- 不使用配置文件的程序去掉 `-c, --config` 与 `CONFIG_FILE` 两行，并同时去掉对应的解析代码与 `configPath`。
- 不新增 `-v`、`-V`、`version` 子命令或 `help` 子命令。

### 10.4 参数

- 全局选项用标准库 `flag.NewFlagSet(<程序名>, flag.ContinueOnError)` 解析并 `SetOutput(io.Discard)`。按 `flag` 包语义，单横线与双横线等价，`-c file`、`-c=file`、`--config file`、`--config=file` 均可。
- 服务的全局选项只有 `-c` / `--config`、`-h` / `--help`、`--version`；出现任何位置参数（包括 `--` 之后）为用法错误 `unexpected argument "<值>"`。
- `--config` 的值为空或全空白为用法错误；多次给出且值相同视为一次，值不同为用法错误。
- 凡是不需要 I/O 就能发现的参数错误都是用法错误。

### 10.5 处理顺序与输出

处理顺序：解析全局参数 → `--help` → `--version` → （命令行工具）解析子命令及其参数 → 子命令 `--help` → 选择配置 → 加载配置 → 初始化日志 → 运行。`--help` 与 `--version` 不读环境变量、配置文件、数据库或网络，同时给出时 `--help` 优先。

| 情况 | stdout | stderr | 退出码 |
| --- | --- | --- | --- |
| `-h` / `--help`，子命令 `--help` | 帮助文本 | 空 | 0 |
| `--version` | `<程序名> <版本>` 一行 | 空 | 0 |
| 用法错误 | 空 | `<程序名>: <错误>`，换行后 `Run '<程序名> --help' for usage.`；子命令的错误为 `Run '<程序名> <实际命令名> --help' for usage.` | 2 |
| 日志就绪前的失败（选择、加载、校验配置，打开日志） | 空 | 一行 `<程序名>: <阶段>: <错误>`，阶段如 `config`、`log` | 1 |
| 日志就绪后的失败 | 由日志记录，不再另外打印到 stderr | — | 1 |
| 服务收到 SIGINT / SIGTERM 后正常关闭 | — | — | 0 |

错误文本不带颜色与堆栈。

### 10.6 配置选择

`cli.go` 的 `configPath` 是唯一读取 `CONFIG_FILE` 的位置，`internal/config` 不自行读取它：

1. `--config` 非空 → 返回该值，`explicit = true`。
2. `CONFIG_FILE` 已设置：值为空或全空白 → 错误 `CONFIG_FILE is set but empty`（退出码 1）；否则返回该值，`explicit = true`。
3. 都没有 → `configs/config.local.yaml`，`explicit = false`。

`run` 用结果构造 `config.Source`（第 7 节）。相对路径基准按 [目录规范.md](目录规范.md) 第 7.1 节，字段级环境变量覆盖按本文第 7 节与目录规范第 7.1 节，并在 README“配置约定”中写明。

### 10.7 版本与服务进程

- `--version` 输出 `<程序名> <版本>[ (revision=<12 位提交>[, modified])]`；版本串已含提交号（伪版本）时不重复 `revision=`。例如：tag 构建 `harbor-console v1.2.0 (revision=0123456789ab)`；tag 之后的提交 `harbor-console v1.0.3-0.20261023134902-c73ec34dab40+dirty (modified)`（从未打 tag 的仓库为 `v0.0.0-...`）；无 VCS 信息 `harbor-console dev`。
- 版本来源与优先级按 [目录规范.md](目录规范.md) 第 8.1 节，只在 `main` 包的 `version` 变量注入。应用需要版本时，`run` 把 `currentBuild()` 的字段以参数形式传给应用装配（如 `app.Build{Version, Revision, Time, Modified}`），其他包不调用 `debug.ReadBuildInfo`。
- 信号使用 `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)`；用 `context.AfterFunc(ctx, stop)` 在第一个信号到达后恢复默认信号处理，使第二次信号可以强制退出。之后在有限超时内关闭：调用 `Shutdown` 并等待其返回、处理其错误，超时后 `Close`；`Serve` 返回 `http.ErrServerClosed` 视为正常。
- 先监听端口再启动后台任务，端口被占用时立即失败。
- 启动时记录 `service_starting`（含 `version`），监听成功后记录 `service_listening`（含 `listen`）；开始关闭时记录 `service_stopping`，正常退出前记录 `service_stopped`。

### 10.8 命令行工具

- 可以定义自己的选项和位置参数；`-c` / `--config`、`-h` / `--help`、`--version` 若提供，含义与上文一致。
- 带子命令时用法为 `<程序名> [options] <command> [arguments]`。全局选项写在命令之前；缺少命令为 `missing command`，未知命令为 `unknown command "<值>"`。每个子命令用自己的 `FlagSet`（同样 `ContinueOnError` 与 `SetOutput(io.Discard)`）解析，支持 `-h` / `--help`；子命令参数错误退出 2。帮助文本在 `Environment:` 之后增加：

```text
Commands:
  <命令>, <命令>, ...

Options go before the command; command flags go after it.
Run '<程序名> <command> --help' for command usage.
```

- 命令结果写 stdout（机器可读时为每行一个 JSON 对象），日志与诊断写 stderr。
- 被 SIGINT / SIGTERM 中断、任务未完成时退出 1。

## 附录 A：服务入口模板

仅在新建或修改 `cmd/` 时阅读。`<name>` 替换为程序名，`<module>` 替换为模块路径，`<summary>` 替换为帮助文本中的一句话说明；应用装配部分按项目实现。

`cli.go`：

```go
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

const usage = `<name> - <summary>

Usage:
  <name> [options]

Options:
  -c, --config <file>  Configuration file (default: configs/config.local.yaml)
  -h, --help           Print this help and exit
      --version        Print version information and exit

Environment:
  CONFIG_FILE          Configuration file, used when --config is not given
`

const defaultConfigFile = "configs/config.local.yaml"

type options struct {
	configFile string // empty: CONFIG_FILE or the default file
	help       bool
	version    bool
}

// parseArgs accepts -x and --x spellings, "--config file" and "--config=file".
func parseArgs(args []string) (options, error) {
	var opts options
	flags := flag.NewFlagSet("<name>", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	setConfig := func(v string) error {
		if strings.TrimSpace(v) == "" {
			return errors.New("configuration path must not be empty")
		}
		if opts.configFile != "" && opts.configFile != v {
			return fmt.Errorf("different files given for -c/--config: %q and %q", opts.configFile, v)
		}
		opts.configFile = v
		return nil
	}
	flags.Func("c", "", setConfig)
	flags.Func("config", "", setConfig)
	flags.BoolVar(&opts.help, "h", false, "")
	flags.BoolVar(&opts.help, "help", false, "")
	flags.BoolVar(&opts.version, "version", false, "")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() > 0 {
		return options{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	return opts, nil
}

// configPath returns --config, then CONFIG_FILE, then the default file.
// explicit reports whether the user chose the file.
func (o options) configPath(lookupEnv func(string) (string, bool)) (path string, explicit bool, err error) {
	if o.configFile != "" {
		return o.configFile, true, nil
	}
	if path, ok := lookupEnv("CONFIG_FILE"); ok {
		if strings.TrimSpace(path) == "" {
			return "", false, errors.New("CONFIG_FILE is set but empty")
		}
		return path, true, nil
	}
	return defaultConfigFile, false, nil
}
```

`main.go`：

```go
// Command <name> <verb phrase, e.g. "serves the Harbor administration console">.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"<module>/internal/app"
	"<module>/internal/config"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run reports usage and startup errors as plain text on stderr; once the
// logger is ready, failures are logged.
func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "<name>: %v\nRun '<name> --help' for usage.\n", err)
		return exitUsage
	}
	build := currentBuild()
	switch {
	case opts.help:
		fmt.Fprint(stdout, usage)
		return exitOK
	case opts.version:
		fmt.Fprintln(stdout, "<name>", build)
		return exitOK
	}
	path, explicit, err := opts.configPath(os.LookupEnv)
	if err != nil {
		fmt.Fprintf(stderr, "<name>: config: %v\n", err)
		return exitFailure
	}
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "<name>: config: %v\n", err)
		return exitFailure
	}
	cfg, err := config.Load(config.Source{Path: path, Explicit: explicit, WorkDir: workDir, LookupEnv: os.LookupEnv})
	if err != nil {
		fmt.Fprintf(stderr, "<name>: config: %v\n", err)
		return exitFailure
	}
	logger, closeLog, err := newLogger(cfg.Log, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "<name>: log: %v\n", err)
		return exitFailure
	}
	defer func() {
		if err := closeLog(); err != nil {
			fmt.Fprintf(stderr, "<name>: log: %v\n", err)
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop) // a second signal terminates the process
	logger.Info("service_starting", "version", build.String())
	err = app.Run(ctx, cfg, logger, app.Build{
		Version:  build.Version,
		Revision: build.Revision,
		Time:     build.Time,
		Modified: build.Modified,
	})
	if err != nil {
		logger.Error("service_failed", "error", err)
		return exitFailure
	}
	logger.Info("service_stopped")
	return exitOK
}
```

`app.Run` 在 `ctx` 结束后按第 10.7 节关闭；`context.AfterFunc(ctx, stop)` 在第一个信号到达后恢复默认信号处理，第二次信号即可强制退出。`newLogger` 放在 `log.go`，返回 `(*slog.Logger, func() error, error)`；输出到 stdout 时关闭函数为空操作。

子命令程序的 `parseArgs` 返回 `(options, []string, error)`，第二个值是命令及其参数；每个子命令在单独的函数中创建自己的 `FlagSet` 并解析剩余参数。

## 附录 B：`version.go`

逐字使用，只替换注释中的程序名：

```go
package main

import (
	"cmp"
	"runtime/debug"
	"strings"
)

// version may be set for builds without VCS metadata, such as Docker builds:
//
//	go build -ldflags "-X main.version=v1.0.0" ./cmd/<name>
//
// Otherwise the module version Go stamps from VCS is used, then the revision.
var version = "dev"

// buildInfo describes the running binary.
type buildInfo struct {
	Version  string // release, module version, short revision or "dev"
	Revision string // short VCS revision; empty when unknown
	Time     string // VCS commit time in RFC 3339; empty when unknown
	Modified bool   // built from a working tree with uncommitted changes
}

func currentBuild() buildInfo {
	info, _ := debug.ReadBuildInfo()
	return readBuild(version, info)
}

func readBuild(release string, info *debug.BuildInfo) buildInfo {
	var b buildInfo
	var moduleVersion string
	if info != nil {
		moduleVersion = info.Main.Version
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				b.Revision = s.Value[:min(12, len(s.Value))]
			case "vcs.time":
				b.Time = s.Value
			case "vcs.modified":
				b.Modified = s.Value == "true"
			}
		}
	}
	b.Version = cmp.Or(usableVersion(release), usableVersion(moduleVersion), b.Revision, "dev")
	return b
}

func usableVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "dev" || v == "(devel)" {
		return ""
	}
	return v
}

// String formats the version as "v1.0.0 (revision=0123456789ab, modified)".
func (b buildInfo) String() string {
	var details []string
	// A VCS-derived module version already embeds the revision.
	if b.Revision != "" && !strings.Contains(b.Version, b.Revision) {
		details = append(details, "revision="+b.Revision)
	}
	if b.Modified {
		details = append(details, "modified")
	}
	if len(details) == 0 {
		return b.Version
	}
	return b.Version + " (" + strings.Join(details, ", ") + ")"
}
```

## 附录 C：版本测试

`version_test.go` 与 `version_build_test.go` 逐字使用，不需要替换。`version_build_test.go` 在临时 Git 仓库中真实构建并运行，用 `go test -tags integration ./cmd/<name>` 执行。

`version_test.go`：

```go
package main

import (
	"runtime/debug"
	"testing"
)

func TestReadBuild(t *testing.T) {
	t.Parallel()
	const revision = "0123456789abcdef0123456789abcdef01234567"
	vcs := func(modified string) []debug.BuildSetting {
		return []debug.BuildSetting{
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.time", Value: "2026-10-01T12:00:00Z"},
			{Key: "vcs.modified", Value: modified},
		}
	}
	for _, tc := range []struct {
		name    string
		release string
		info    *debug.BuildInfo
		want    string
	}{
		{"no metadata", "dev", nil, "dev"},
		{"release without metadata", "v1.2.0", nil, "v1.2.0"},
		{"release with VCS", "v1.2.0", &debug.BuildInfo{Settings: vcs("false")}, "v1.2.0 (revision=0123456789ab)"},
		{"module tag", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.0"}, Settings: vcs("false")}, "v1.2.0 (revision=0123456789ab)"},
		{"pseudo-version", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261001120000-0123456789ab"}, Settings: vcs("false")}, "v0.0.0-20261001120000-0123456789ab"},
		{"dirty pseudo-version", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261001120000-0123456789ab+dirty"}, Settings: vcs("true")}, "v0.0.0-20261001120000-0123456789ab+dirty (modified)"},
		{"devel checkout", "dev", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: vcs("true")}, "0123456789ab (modified)"},
		{"blank release", " ", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := readBuild(tc.release, tc.info).String(); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadBuildKeepsCommitTime(t *testing.T) {
	t.Parallel()
	got := readBuild("dev", &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.time", Value: "2026-10-01T12:00:00Z"}}})
	want := buildInfo{Version: "dev", Time: "2026-10-01T12:00:00Z"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
```

`version_build_test.go`：

```go
//go:build integration

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildVersion(t *testing.T) {
	const (
		commandTimeout = 2 * time.Minute
		binaryTimeout  = 10 * time.Second
		pipeWaitDelay  = 5 * time.Second
	)
	// Keep all commits, tags, binaries and source archives outside the project.
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	archive := filepath.Join(base, "archive")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	code, err := os.ReadFile("version.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{repo, archive} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string][]byte{
			"go.mod":     []byte("module fixture\n\ngo 1.24\n"),
			"version.go": code,
			"main.go":    []byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println(currentBuild()) }\n"),
		} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	// command runs argv[0] with the remaining arguments in dir.
	command := func(t *testing.T, dir string, argv ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), commandTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = dir
		// Descendants retaining output pipes must not outlive the command's wait.
		cmd.WaitDelay = pipeWaitDelay
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%q: %v\n%s", argv, errors.Join(err, ctx.Err()), out)
		}
		return strings.TrimSpace(string(out))
	}
	git := func(args ...string) string {
		t.Helper()
		return command(t, repo, append([]string{"git"}, args...)...)
	}
	git("init", "-q")
	git("config", "user.name", "Version Fixture")
	git("config", "user.email", "version@example.invalid")
	git("config", "commit.gpgsign", "false")
	git("config", "core.hooksPath", os.DevNull)
	git("add", ".")
	git("commit", "-qm", "fixture")
	revision := git("rev-parse", "--short=12", "HEAD")
	// buildCase expects the output to equal prefix when suffix is empty,
	// otherwise to start with prefix and end with suffix.
	type buildCase struct {
		name   string
		dir    string
		prefix string
		suffix string
		flags  []string
	}
	check := func(tc buildCase) {
		t.Helper()
		t.Run(tc.name, func(t *testing.T) {
			binary := filepath.Join(base, "binary")
			argv := append([]string{"go", "build", "-o", binary}, tc.flags...)
			command(t, tc.dir, append(argv, ".")...)
			// No source directory or Git executable is available at runtime.
			ctx, cancel := context.WithTimeout(t.Context(), binaryTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary)
			cmd.Dir = base
			cmd.Env = []string{"PATH=" + filepath.Join(base, "missing")}
			cmd.WaitDelay = pipeWaitDelay
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run binary: %v\n%s", errors.Join(err, ctx.Err()), out)
			}
			got := strings.TrimSpace(string(out))
			if (tc.suffix == "" && got != tc.prefix) || !strings.HasPrefix(got, tc.prefix) || !strings.HasSuffix(got, tc.suffix) {
				t.Fatalf("got %q, want prefix %q and suffix %q", got, tc.prefix, tc.suffix)
			}
			t.Log(got)
		})
	}
	clean := " (revision=" + revision + ")"
	check(buildCase{name: "no tag", dir: repo, prefix: "v0.0.0-", suffix: "-" + revision})
	git("tag", "1.1")
	check(buildCase{name: "legacy tag ignored", dir: repo, prefix: "v0.0.0-", suffix: "-" + revision})
	git("tag", "v1.2.3")
	check(buildCase{name: "standard tag", dir: repo, prefix: "v1.2.3" + clean})
	for _, value := range []string{"", "dev", "(devel)"} {
		check(buildCase{name: "placeholder " + value, dir: repo, prefix: "v1.2.3" + clean, flags: []string{"-ldflags", "-X main.version=" + value}})
	}
	check(buildCase{name: "explicit override", dir: repo, prefix: "custom-build" + clean, flags: []string{"-ldflags", "-X main.version=custom-build"}})
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	modified := " (revision=" + revision + ", modified)"
	check(buildCase{name: "dirty tag", dir: repo, prefix: "v1.2.3+dirty" + modified})
	check(buildCase{name: "dirty override", dir: repo, prefix: "custom-build" + modified, flags: []string{"-ldflags", "-X main.version=custom-build"}})
	git("add", ".")
	git("commit", "-qm", "after tag")
	revision = git("rev-parse", "--short=12", "HEAD")
	check(buildCase{name: "after tag", dir: repo, prefix: "v1.2.4-0.", suffix: "-" + revision})
	check(buildCase{name: "disabled VCS", dir: repo, prefix: "dev", flags: []string{"-buildvcs=false"}})
	check(buildCase{name: "source archive", dir: archive, prefix: "dev"})
	check(buildCase{name: "archive override", dir: archive, prefix: "v1.2.3", flags: []string{"-ldflags", "-X main.version=v1.2.3"}})
}
```

其余入口测试至少覆盖：

- `cli_test.go`：第 10.4 节的每条接受与拒绝情形；`configPath` 的参数优先、环境变量、默认文件与空 `CONFIG_FILE`。
- `main_test.go`：用法错误退出 2、stdout 为空、stderr 以 `<name>: ` 开头并以 `Run '<name> --help' for usage.` 结尾；`CONFIG_FILE` 指向不存在的文件时 `--help` / `--version` 仍退出 0 且 stderr 为空；配置失败退出 1。
