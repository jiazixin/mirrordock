import { cancelPageRequests } from './api'
import { createRouter, createWebHistory } from 'vue-router'
const Overview = () => import('../features/overview/OverviewPage.vue')
const Sources = () => import('../features/sources/SourcesPage.vue')
const Batches = () => import('../features/runs/BatchesPage.vue')
const Records = () => import('../features/runs/RecordsPage.vue')
const Schedules = () => import('../features/schedules/SchedulesPage.vue')
const Analytics = () => import('../features/analytics/AnalyticsPage.vue')
const Settings = () => import('../features/settings/SettingsPage.vue')

export const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    { path: '/', redirect: '/overview' },
    { path: '/overview', component: Overview, meta: { title: '概览' } },
    { path: '/sources', component: Sources, meta: { title: '同步源' } },
    { path: '/batches', component: Batches, meta: { title: '批次记录' } },
    { path: '/records', component: Records, meta: { title: '运行记录' } },
    { path: '/schedules', component: Schedules, meta: { title: '计划管理' } },
    {
      path: '/analytics/rsync',
      component: Analytics,
      props: { kind: 'rsync' },
      meta: { title: 'Rsync 分析', view: 'analytics' },
    },
    {
      path: '/analytics/reposync',
      component: Analytics,
      props: { kind: 'reposync' },
      meta: { title: 'Reposync 分析', view: 'analytics' },
    },
    {
      path: '/analytics/github',
      component: Analytics,
      props: { kind: 'github_release' },
      meta: { title: 'Release 分析', view: 'analytics' },
    },
    { path: '/settings', component: Settings, meta: { title: '系统设置' } },
    { path: '/:pathMatch(.*)*', redirect: '/overview' },
  ],
})

router.beforeEach((to, from) => {
  if (to.path !== from.path) cancelPageRequests()
})
