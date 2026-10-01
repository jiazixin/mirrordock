import { createRouter, createWebHistory } from 'vue-router'
import Sources from '../features/sources/Sources.vue'
import Batches from '../features/runs/Batches.vue'
import Records from '../features/runs/Records.vue'
import Schedules from '../features/schedules/Schedules.vue'
import Analytics from '../features/analytics/Analytics.vue'
import Settings from '../features/settings/Settings.vue'

export const router = createRouter({ history: createWebHistory('/admin/'), routes: [{ path: '/', redirect: '/sources' }, { path: '/sources', component: Sources, meta: { title: '同步源' } }, { path: '/batches', component: Batches, meta: { title: '批次记录' } }, { path: '/records', component: Records, meta: { title: '运行记录' } }, { path: '/schedules', component: Schedules, meta: { title: '计划管理' } }, { path: '/analytics/rsync', component: Analytics, props: { kind: 'rsync' }, meta: { title: 'Rsync 分析' } }, { path: '/analytics/reposync', component: Analytics, props: { kind: 'reposync' }, meta: { title: 'Reposync分析' } }, { path: '/analytics/github', component: Analytics, props: { kind: 'github_release' }, meta: { title: 'Release分析' } }, { path: '/settings', component: Settings, meta: { title: '系统设置' } }, { path: '/:pathMatch(.*)*', redirect: '/sources' }] })
