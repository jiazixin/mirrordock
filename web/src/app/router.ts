import { createRouter, createWebHistory } from 'vue-router'
import Sources from '../features/sources/Sources.vue'
import Batches from '../features/runs/Batches.vue'
import Records from '../features/runs/Records.vue'
import Schedules from '../features/schedules/Schedules.vue'
import Settings from '../features/settings/Settings.vue'
import SystemConfiguration from '../features/system/SystemConfiguration.vue'

export const router = createRouter({ history: createWebHistory('/admin/'), routes: [{ path: '/', redirect: '/sources' }, { path: '/sources', component: Sources, meta: { title: '同步源' } }, { path: '/batches', component: Batches, meta: { title: '批次记录' } }, { path: '/records', component: Records, meta: { title: '运行记录' } }, { path: '/schedules', component: Schedules, meta: { title: '计划管理' } }, { path: '/settings', component: Settings, meta: { title: '系统设置' } }, { path: '/system', component: SystemConfiguration, meta: { title: '系统配置' } }, { path: '/:pathMatch(.*)*', redirect: '/sources' }] })
