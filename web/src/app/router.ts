import { createRouter, createWebHistory } from 'vue-router'
const Overview = () => import('../features/overview/Overview.vue')
const Sources = () => import('../features/sources/Sources.vue')
const Batches = () => import('../features/runs/Batches.vue')
const Records = () => import('../features/runs/Records.vue')
const Schedules = () => import('../features/schedules/Schedules.vue')
const Analytics = () => import('../features/analytics/Analytics.vue')
const Settings = () => import('../features/settings/Settings.vue')

export const router = createRouter({
  history: createWebHistory('/admin/'),
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
      meta: { title: 'Rsync 分析' },
    },
    {
      path: '/analytics/reposync',
      component: Analytics,
      props: { kind: 'reposync' },
      meta: { title: 'Reposync分析' },
    },
    {
      path: '/analytics/github',
      component: Analytics,
      props: { kind: 'github_release' },
      meta: { title: 'Release分析' },
    },
    { path: '/settings', component: Settings, meta: { title: '系统设置' } },
    { path: '/:pathMatch(.*)*', redirect: '/overview' },
  ],
})
