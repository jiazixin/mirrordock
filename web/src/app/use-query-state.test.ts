import { describe, expect, it } from 'vitest'
import { createApp, effectScope, nextTick, ref } from 'vue'
import { createMemoryHistory, createRouter, routeLocationKey, routerKey } from 'vue-router'
import { useQueryState } from './use-query-state'

it('restores filters and page sizes before page and releases URL updates after scope disposal', async () => {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/records', component: { template: '<div />' } }],
  })
  await router.push('/records?search=history&page=3&pageSize=20')
  // eslint-disable-next-line vue/one-component-per-file -- Each test needs an isolated injection context.
  const app = createApp({})
  const route = router.currentRoute.value
  app.provide(routerKey, router)
  app.provide(routeLocationKey, route)
  const scope = effectScope()
  const search = ref(''),
    page = ref(1),
    pageSize = ref(50)
  app.runWithContext(() =>
    scope.run(() => {
      useQueryState({ search, page, pageSize })
    }),
  )
  expect([search.value, page.value, pageSize.value]).toEqual(['history', 3, 20])
  scope.stop()
  search.value = 'changed-after-disposal'
  await nextTick()
  expect(router.currentRoute.value.query.search).toBe('history')
})

describe('query validation', () => {
  it('ignores malformed and out-of-range pagination', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: { template: '<div />' } }],
    })
    await router.push('/?page=-4&pageSize=100000&type=unknown')
    // eslint-disable-next-line vue/one-component-per-file -- Each test needs an isolated injection context.
    const app = createApp({})
    app.provide(routerKey, router)
    app.provide(routeLocationKey, router.currentRoute.value)
    const scope = effectScope()
    const page = ref(1),
      pageSize = ref(50),
      type = ref('')
    app.runWithContext(() =>
      scope.run(() => {
        useQueryState({ page, pageSize, type }, { type: ['', 'rsync'] })
      }),
    )
    expect([page.value, pageSize.value, type.value]).toEqual([1, 50, ''])
    scope.stop()
  })
})
