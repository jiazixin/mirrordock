<script setup lang="ts">
import { useRoute } from 'vue-router'
import {
  ArrowUpRight,
  CalendarClock,
  ChartColumn,
  FileText,
  House,
  Layers,
  ListChecks,
  LogOut,
  Menu,
  SlidersHorizontal,
  X,
} from '@lucide/vue'
import { ref, watch, onBeforeUnmount } from 'vue'
import ToastStack from '../components/ToastStack.vue'
import { state, dismissToast } from '../app/state'
import logo from '../assets/logo.svg'
const emit = defineEmits<{ logout: [] }>()
const route = useRoute()
const mobile = ref(false)
const menuButton = ref<HTMLButtonElement>()
function closeOnEscape(event: KeyboardEvent) {
  if (event.key !== 'Escape' || !mobile.value) return
  mobile.value = false
  menuButton.value?.focus()
}
document.addEventListener('keydown', closeOnEscape)
onBeforeUnmount(() => {
  state.toasts = []
  document.removeEventListener('keydown', closeOnEscape)
})
watch(
  () => route.fullPath,
  () => {
    mobile.value = false
  },
)
interface NavLink {
  path: string
  label: string
  icon: typeof House
  section?: string
}
const groups: { label: string; links: NavLink[] }[] = [
  {
    label: '工作台',
    links: [
      { path: '/overview', label: '概览', icon: House },
      { path: '/sources', label: '同步源', icon: Layers },
    ],
  },
  {
    label: '任务记录',
    links: [
      { path: '/batches', label: '批次记录', icon: ListChecks },
      { path: '/records', label: '运行记录', icon: FileText },
    ],
  },
  {
    label: '运行管理',
    links: [{ path: '/schedules', label: '计划管理', icon: CalendarClock }],
  },
  {
    label: '分析',
    links: [
      { path: '/analytics/rsync', label: '数据分析', icon: ChartColumn, section: '/analytics/' },
    ],
  },
  {
    label: '系统',
    links: [{ path: '/settings', label: '系统设置', icon: SlidersHorizontal }],
  },
]
const current = (link: NavLink) =>
  link.section ? route.path.startsWith(link.section) : route.path === link.path
</script>

<template>
  <ToastStack
    :items="state.toasts"
    @dismiss="dismissToast"
  />
  <div class="app-shell">
    <aside class="sidebar">
      <div class="sidebar-top">
        <RouterLink
          class="brand"
          to="/overview"
          ><img
            :src="logo"
            alt=""
          /><strong>MirrorDock</strong></RouterLink
        >
        <button
          ref="menuButton"
          class="icon-button mobile-menu"
          type="button"
          :aria-label="mobile ? '关闭导航' : '打开导航'"
          aria-controls="primary-navigation"
          :aria-expanded="mobile"
          @click="mobile = !mobile"
        >
          <component
            :is="mobile ? X : Menu"
            :size="20"
          />
        </button>
      </div>
      <div
        id="primary-navigation"
        class="primary-nav"
        :class="{ open: mobile }"
      >
        <nav aria-label="主导航">
          <template
            v-for="group in groups"
            :key="group.label"
          >
            <span
              class="nav-group"
              aria-hidden="true"
              >{{ group.label }}</span
            >
            <RouterLink
              v-for="link in group.links"
              :key="link.path"
              :to="link.path"
              :class="{ active: link.section && current(link) }"
              :aria-current="current(link) ? 'page' : undefined"
              @click="mobile = false"
              ><component
                :is="link.icon"
                :size="19"
                :stroke-width="1.7"
              />{{ link.label }}</RouterLink
            >
          </template>
        </nav>
        <div class="sidebar-foot">
          <div
            class="live-status"
            :class="{ on: state.connected }"
          >
            <span>{{ state.connected ? '实时推送已连接' : '实时连接已断开，正在重连' }}</span
            ><a
              class="header-link"
              href="/"
              target="_blank"
              rel="noopener"
              >镜像目录<ArrowUpRight :size="15"
            /></a>
          </div>
          <div class="account">
            <span
              class="avatar"
              aria-hidden="true"
              >{{ state.username.slice(0, 1).toUpperCase() }}</span
            >
            <div class="account-name">
              <strong :title="state.username">{{ state.username }}</strong
              ><small>管理员</small>
            </div>
            <button
              class="icon-button"
              type="button"
              aria-label="退出登录"
              title="退出登录"
              @click="emit('logout')"
            >
              <LogOut :size="19" />
            </button>
          </div>
        </div>
      </div>
    </aside>
    <div
      v-if="mobile"
      class="nav-scrim"
      @click="mobile = false"
    ></div>
    <main class="main-shell">
      <div class="page-content">
        <RouterView />
      </div>
    </main>
  </div>
</template>
