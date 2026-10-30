# Web 代码规范

基线版本：1.0.0。适用范围：`web/` 下的 TypeScript Web 应用中与框架无关的部分。先读 [代码规范.md](代码规范.md)；Vue 应用再读 `Vue代码规范.md`，React 应用再读 `React代码规范.md`。目录归属见 [目录规范.md](目录规范.md) 第 5 节。

## 1. 工具链

- 新应用的包管理器为 npm，提交 `package-lock.json`，CI 使用 `npm ci`；既有应用沿用其锁文件确定的包管理器，切换是单独任务。`package.json` 声明 `engines.node`，与 CI 使用的 Node 版本一致。
- 构建用 Vite，格式化用 Prettier，脚本与样式检查用 ESLint flat config 与 Stylelint，单元测试用 Vitest，浏览器测试用 Playwright。
- `web/package.json` 的脚本名称固定，按需提供，不另起同义名称：

| 脚本 | 内容 |
| --- | --- |
| `dev` | 开发服务器 |
| `build` | 生产构建，不隐式执行检查 |
| `typecheck` | 使用 project references 时为 `tsc -b`（Vue 为 `vue-tsc --build`）；单一 tsconfig 时为 `tsc --noEmit -p tsconfig.json`（Vue 为 `vue-tsc --noEmit`） |
| `lint` | `eslint . --max-warnings 0 && stylelint "src/**/*.{css,vue}" --max-warnings 0` |
| `format` / `format:check` | `prettier --write .` / `prettier --check .` |
| `test` | `vitest run` |
| `test:e2e` | `playwright test` |

- CI 依次显式调用 `format:check`、`lint`、`typecheck`、`test`、`build`，需要时再调用 `test:e2e`；另运行 `npm audit --audit-level=high --omit=dev`。
- `vite.config` 中显式设置 `build.target`，与项目文档登记的浏览器支持范围一致；生产构建不发布 sourcemap。
- 不从 CDN 加载脚本、样式或字体，全部资源随构建打包，使 `default-src 'self'` 的 CSP 可以成立。

## 2. TypeScript

每个包含源码、测试或工具配置的 tsconfig 都至少开启：

```json
{
  "strict": true,
  "noUncheckedIndexedAccess": true,
  "noUnusedLocals": true,
  "noUnusedParameters": true,
  "noImplicitOverride": true,
  "noFallthroughCasesInSwitch": true,
  "verbatimModuleSyntax": true,
  "erasableSyntaxOnly": true
}
```

- 禁止 `any`，包括泛型默认值 `T = any`；不可信数据先当作 `unknown`。
- 禁止非空断言 `!`；用条件收窄、提前返回或边界校验得到确定类型。
- 类型断言 `as` 只用于已校验的边界和确有必要的 DOM 操作；检查字面量形状用 `satisfies`。
- 对象类型用 `type` 声明；`interface` 只用于 `.d.ts` 文件中的模块扩充与全局声明（`declare module`、`declare global`、`ImportMetaEnv`）。不用 `enum`，用字面量联合类型及其 `as const` 常量表。
- 类型导入使用 `import type`。
- 只使用相对路径导入，不定义路径别名。业务域之间只通过公开入口协作，见目录规范第 5.1 节。
- 不写嵌套三元表达式；两个以上分支改用提前返回、`switch` 或查找表。

## 3. 格式化与静态检查

- 新应用的 Prettier 配置 `web/.prettierrc.json` 固定为：

```json
{
  "semi": false,
  "singleQuote": true,
  "printWidth": 100
}
```

  其余使用 Prettier 默认值；格式化范围包含 `src/`、`e2e/`、`tests/` 与工具配置文件，`.prettierignore` 只排除生成物。既有应用的 Prettier 配置与此不同时，在切换前沿用原配置；切换是一次单独的全量格式化改动，不在功能任务中修改 `.prettierrc.json`。
- ESLint 至少启用 `typescript-eslint` 的 `strictTypeChecked`，类型感知配置使用 `parserOptions.projectService: true`，并显式开启：

```js
'@typescript-eslint/no-explicit-any': 'error',
'@typescript-eslint/no-non-null-assertion': 'error',
'@typescript-eslint/no-floating-promises': 'error',
'@typescript-eslint/no-misused-promises': 'error',
'@typescript-eslint/consistent-type-imports': 'error',
'@typescript-eslint/consistent-type-definitions': ['error', 'type'],
'no-console': ['error', { allow: ['warn', 'error'] }],
```

  `files: ['**/*.d.ts']` 中关闭 `@typescript-eslint/consistent-type-definitions`。框架插件见各框架规范。
- Stylelint 在 `src/styles/` 之外开启 `color-no-hex`、`color-named: 'never'` 与 `function-disallowed-list: ['rgb', 'rgba', 'hsl', 'hsla', 'hwb', 'lab', 'lch', 'oklab', 'oklch']`，执行第 8 节的颜色规则。
- 抑制注释必须写明规则名和原因：`// eslint-disable-next-line <规则> -- <原因>`。禁止文件级或全局关闭规则来通过检查。

## 4. 命名

- 新应用：组件文件 PascalCase（`ProjectTable.vue`、`ProjectTable.tsx`），路由页面组件以 `Page` 结尾（`LibraryPage`）；非组件的 `.ts` 文件 kebab-case（`format-bytes.ts`）；hook / composable 文件为 `use-<名称>.ts`，导出 `use<名称>`。既有应用沿用其已有的文件命名方式（目录规范第 4 节）。
- 变量与函数 camelCase，类型与组件 PascalCase；布尔值以 `is` / `has` / `can` / `should` 开头。
- 接口字段在客户端保留服务端原名，不在客户端改名；只在展示层组合成界面需要的形状。

## 5. 请求与错误

- 每个应用只有一个请求包装，放 `src/app/`（如 `src/app/api.ts`）；业务域与布局禁止直接调用 `fetch`。
- 包装函数参数为对象：

```ts
api<T>(path: string, options?: { method?: string; body?: unknown; signal?: AbortSignal; headers?: Record<string, string> }): Promise<T>
```

- 必须显式给出响应类型 `T`。响应体先按 `unknown` 读取，在包装层集中完成类型转换。来自外部服务、用户上传等不可信来源的数据必须用 schema（Zod）校验；同仓库 Go 后端的响应可在包装层断言，契约由测试样例覆盖。
- 失败一律抛出 `ApiError`：

```ts
type ApiErrorInit = {
  status: number // HTTP status; 0 for client-side failures
  code: string // server error.code, or a client code from 代码规范.md §5.3
  requestId: string // X-Request-ID; empty when unavailable
  details?: unknown
  retryAfterMs?: number // parsed Retry-After
}
```

  `ApiError.message` 保持英文开发者文本，不展示给用户。取消请求不转换为 `ApiError`，保持原生 `AbortError`。
- 用户可见文案只通过 `src/app/errors.ts` 中唯一的 `errorText(error, messages?)` 获取：先查调用方传入的业务域错误码表，再查通用错误码表，最后按状态码兜底。`src/app/api.ts`、`src/app/errors.ts` 等基础模块不导入业务域（目录规范第 5.1 节），业务域把自己的错误码表作为参数传入；路由表等装配模块可以导入业务域页面。
- 会话失效（401）与请求超时在包装层统一处理。
- 所有请求支持 `AbortSignal`；页面卸载、查询条件变化时取消旧请求，旧响应不得覆盖新查询的结果。重试规则见 [代码规范.md](代码规范.md) 第 5.3 节。
- 令牌不存放在 `localStorage` / `sessionStorage`；浏览器会话使用 `HttpOnly` Cookie。
- 跳转到服务端或 URL 参数给出的地址前，校验其为同源或白名单地址，防止开放重定向。

## 6. 错误与状态展示

- `catch` 变量按 `unknown` 处理，用 `instanceof` 收窄；禁止 `(e as Error)`。
- 加载失败在页面内显示 `role="alert"` 的错误提示和重试按钮，保留已经读到的数据；加载中、空、错误三种状态必须可区分，不能把加载失败显示成空列表。
- 400 校验错误按 `details` 中的字段路径显示在对应表单项旁，不只弹提示。
- 写操作的结果用全局提示（toast，`role="status"`）反馈；同一应用只有一套提示机制。
- 未处理的 Promise 必须被 `await`、返回，或显式 `void` 并处理错误。

## 7. 文本与格式化

- 界面默认中文，`<html lang="zh-CN">`；不引入 i18n 库。产品明确要求多语言时，统一用一套翻译函数，并配套校验缺失翻译的测试。
- 日期、时间、数字、文件大小只通过 `src/lib/format/` 输出；组件内禁止直接调用 `toLocaleString` 等方法。显示时区按项目文档。
- 文件大小按 1024 进制，单位写 `KiB`、`MiB`、`GiB`、`TiB`。空值统一显示 `—`。

## 8. 样式

- 颜色、间距、圆角、阴影、字号、动效时长等设计变量只在 `src/styles/` 中以 `:root` CSS 变量定义；其他位置只引用变量，不写颜色字面量（由第 3 节 Stylelint 规则检查）。
- 声明 `color-scheme`。不强制提供暗色主题；提供时通过 `[data-theme]` 覆盖同一组变量，不复制组件样式。
- 组件私有样式就近放置（写法见各框架规范）；不在全局样式中写只属于某个组件的选择器。
- 断点集合登记在项目 UI 文档中，新增 `@media` 只使用已登记的值。
- 必须处理 `prefers-reduced-motion`。
- 内联样式只用于运行时计算的几何值（宽度、位置等）。

## 9. 无障碍

- 优先使用语义元素：按钮用 `<button>`，链接用 `<a>`，表单控件配 `<label>`。
- 图标按钮必须有可访问名称。
- 不移除 `:focus-visible` 样式；弹窗打开时焦点移入、关闭后回到触发元素，`Esc` 可关闭。新弹窗优先使用原生 `<dialog>`，否则复用应用内唯一的弹窗组件。
- 触控目标不小于 24×24 CSS px；面向手机的页面不小于 44×44。
- 关键页面在 E2E 中运行 `@axe-core/playwright`，不得有 serious / critical 问题。

## 10. 测试

- 单元测试使用 Vitest + Testing Library（`@testing-library/vue` 或 `@testing-library/react`）+ `@testing-library/user-event`；显式从 `vitest` 导入 `describe`、`it`、`expect`，不开启 `globals`。因此 `src/test/setup.ts` 必须包含：

```ts
import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/<框架>'
import { afterEach } from 'vitest'

afterEach(() => {
  cleanup()
})
```

- 按角色、标签、文本查询元素；只有无法通过可访问属性定位时才用 `data-testid`。不使用 `fireEvent` 模拟用户操作（`user-event` 无法覆盖的事件除外）。
- 至少覆盖：请求包装（错误映射、取消、401）、`errorText`、格式化模块、含逻辑的 hook / composable、关键交互组件。
- Playwright：
  - 定位器优先 `getByRole`、`getByLabel`；CSS 选择器只用于布局几何断言。
  - 禁止用 `waitForTimeout` 等待状态，使用自动等待的断言；确需测时长时写注释。
  - 固定 `locale: 'zh-CN'` 与 `timezoneId`；涉及时间的页面用 `page.clock` 固定时钟。
  - `trace: 'retain-on-failure'`；证据截图写入 `test-results/`。
  - 视觉基线使用 Playwright 默认快照目录，只在与 CI 相同的 Linux 环境中生成和更新，人工评审后提交。
  - 后端使用隔离的临时目录、端口和合成数据，不使用用户配置或真实数据。
