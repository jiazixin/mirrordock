<script setup lang="ts">
import { useRoute, type RouteLocationNormalizedLoaded } from 'vue-router'
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
import ConfirmDialog from '../components/ConfirmDialog.vue'
import ModalTransition from '../components/ModalTransition.vue'
import { state, dismissToast, settleConfirmation } from '../app/state'
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
  settleConfirmation(false)
  state.toasts = []
  document.removeEventListener('keydown', closeOnEscape)
})
watch(
  () => route.fullPath,
  () => {
    mobile.value = false
    // A confirmation belongs to the page that asked; leaving the page declines it.
    settleConfirmation(false)
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
// Routes sharing one view (analytics kinds) keep the component mounted and skip the page transition.
const viewKey = (view: RouteLocationNormalizedLoaded) =>
  typeof view.meta.view === 'string' ? view.meta.view : view.path
const main = ref<HTMLElement>()
// The main column is the desktop scroll container, so a newly entered page starts at its top.
function resetScroll() {
  main.value?.scrollTo({ top: 0 })
}
</script>

<template>
  <ToastStack
    :items="state.toasts"
    @dismiss="dismissToast"
  />
  <ModalTransition>
    <ConfirmDialog
      v-if="state.confirmation"
      :key="state.confirmation.id"
      :title="state.confirmation.title"
      :message="state.confirmation.message"
      :confirm-label="state.confirmation.confirmLabel"
      :cancel-label="state.confirmation.cancelLabel"
      :tone="state.confirmation.tone"
      @settle="settleConfirmation"
    />
  </ModalTransition>
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
          <a
            class="header-link mirror-link"
            href="/"
            target="_blank"
            rel="noopener"
            >镜像目录<ArrowUpRight :size="15"
          /></a>
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
    <main
      ref="main"
      class="main-shell"
    >
      <div class="page-content">
        <RouterView v-slot="{ Component, route: view }">
          <Transition
            name="page"
            mode="out-in"
            @before-enter="resetScroll"
          >
            <div
              v-if="Component"
              :key="viewKey(view)"
            >
              <component :is="Component" />
            </div>
          </Transition>
        </RouterView>
      </div>
    </main>
  </div>
</template>
