# Vue 代码规范

基线版本：1.0.0。适用范围：Vue 3 + TypeScript 的单页 Web 应用（不含 SSR）。先读 [代码规范.md](代码规范.md) 与 [Web代码规范.md](Web代码规范.md)，本文只写 Vue 特有的规则。

## 1. 工具

- Vue 3.5 及以上；类型检查用 `vue-tsc`（命令见 Web 规范第 1 节）。
- ESLint 使用 `@vue/eslint-config-typescript`：

```js
import pluginVue from 'eslint-plugin-vue'
import { defineConfigWithVueTs, vueTsConfigs } from '@vue/eslint-config-typescript'

export default defineConfigWithVueTs(
  pluginVue.configs['flat/recommended'],
  vueTsConfigs.strictTypeChecked,
  {
    rules: {
      // Web 规范第 3 节要求的 @typescript-eslint 规则
      'vue/block-order': ['error', { order: ['script', 'template', 'style'] }],
      'vue/component-api-style': ['error', ['script-setup']],
      'vue/define-props-destructuring': ['error', { destructure: 'always' }],
      'vue/no-v-html': 'error',
    },
  },
)
```

- Stylelint 处理 `.vue` 文件时使用 `postcss-html` 语法。
- 图标统一使用一个图标库（现有项目为 `@lucide/vue`），不混用多个图标库。
- 不引入 UI 组件库；需要时记录为技术决策后引入。

## 2. 单文件组件

- 只使用 `<script setup lang="ts">` 和 Composition API；禁止 Options API、`defineComponent` 对象写法和同一 SFC 中的第二个 `<script>`。需要模块级单例时放在独立的 `.ts` 文件。
- 块顺序固定为 `<script setup>`、`<template>`、`<style scoped>`。
- 模板中使用 PascalCase 标签（`<ProjectTable />`）。
- 模板只做展示与事件绑定；超过一行的表达式移到 `computed` 或函数中。`v-for` 必须带稳定 `:key`，不用数组下标（内容不变的静态列表除外）；不在同一元素上同时使用 `v-if` 和 `v-for`。
- 禁止 `v-html`。确需渲染富文本时，先用净化库处理并注释来源，再以 `eslint-disable-next-line vue/no-v-html -- <原因>` 单处放行。

## 3. Props、Emits 与双向绑定

- Props 用类型声明并解构，默认值使用 Vue 3.5 的解构默认值：

```ts
const { title, size = 'md' } = defineProps<{ title: string; size?: 'sm' | 'md' }>()
```

  解构出的 prop 传给 `watch` 或 composable 时写成 getter：`watch(() => size, ...)`、`useThing(() => size)` 或 `toRef(() => size)`。新模块不使用 `withDefaults`。
- Emits 用元组语法：`defineEmits<{ close: []; select: [id: number] }>()`，事件名 camelCase。
- 双向绑定统一用 `defineModel`，不手写 `modelValue` + `update:modelValue`。
- 不修改 props；需要本地草稿时复制到 `ref`，并明确何时与 props 重新同步。
- 少用 `defineExpose`。通知、确认框等跨组件能力通过全局 composable 提供，不通过模板 ref 调用子组件方法。

## 4. 响应式与状态

- 基本值和需要整体替换的对象用 `ref`；`reactive` 只用于不会被整体替换的对象。`toRefs` 只用于解构 `reactive` 对象。
- 派生数据用 `computed`；`watch` 只用于副作用（请求、存储、DOM），不用 `watch` 同步派生状态。
- 异步 `watch` 必须能取消上一次请求，在第一个 `await` 之前注册清理：

```ts
watch(query, async (value) => {
  const controller = new AbortController()
  onWatcherCleanup(() => controller.abort())
  try {
    items.value = await fetchItems(value, controller.signal)
  } catch (error: unknown) {
    if (controller.signal.aborted) return // superseded by a newer query
    loadError.value = error
  }
})
```

- 深拷贝响应式数据时先 `toRaw()` 再 `structuredClone()`；嵌套的响应式对象需先转为普通对象。
- 默认不引入 Pinia。应用级状态（会话、全局提示、确认框）放 `src/app/` 的模块级 `ref` / `reactive`，只导出只读视图与修改函数；业务状态放所属业务域的 composable。需要 Pinia 时记录为技术决策。
- Composable 命名 `useXxx`，返回 `Ref` / `ComputedRef`，不返回裸值快照；内部注册的定时器、事件监听、订阅在 `onScopeDispose` 中清理。

## 5. 路由

- 只有一个页面的应用不引入路由。两个及以上页面的应用使用 `vue-router` 4，路由表集中在 `src/app/router.ts`，页面组件懒加载（`component: () => import('...')`）。
- 部署方（Go 服务端、nginx 等）为前端路由提供 SPA 回退时使用 `createWebHistory(import.meta.env.BASE_URL)`，否则使用 `createWebHashHistory()`。
- 禁止新写“字符串 page 变量 + `v-if` 链”的手写路由。
- 筛选、分页、选中项等可分享状态放在 URL query 中，刷新后保持。

## 6. 样式

- 组件私有样式写在 `<style scoped>` 中；跨多个组件、只属于一个业务域的样式放该域目录下的 `<业务域>.css`，由使用它的组件导入。
- 穿透子组件样式只用 `:deep()`，且只用于本应用的子组件；不穿透第三方组件的内部结构。

## 7. 测试

- 组件测试使用 `@testing-library/vue`，不直接读取组件实例或内部状态。
- Composable 在 `effectScope` 中运行，并验证 `scope.stop()` 后的清理。
