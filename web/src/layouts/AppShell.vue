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
import {
  state,
  dismissToast,
  settleConfirmation,
  clearToasts,
  perform,
  refresh,
} from '../app/state'
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
  clearToasts()
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
type NavLink = {
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
        <div
          v-if="state.refreshError"
          class="alert error"
          role="alert"
        >
          {{ state.refreshError
          }}<button
            class="button compact"
            :disabled="state.refreshing"
            @click="perform(refresh)"
          >
            重试
          </button>
        </div>
        <p
          v-else-if="state.refreshing"
          role="status"
        >
          正在刷新…
        </p>
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

<style scoped>
.app-shell {
  height: 100vh;
  height: 100dvh;
  overflow: hidden;
  display: grid;
  grid-template-columns: var(--sidebar-width) minmax(0, 1fr);
  grid-template-rows: minmax(0, 1fr);
}
.sidebar {
  position: relative;
  z-index: 40;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-18px);
  padding: var(--space-22px) var(--space-14px) var(--space-16px);
  border-right: 1px solid var(--line);
  background: var(--bg);
}
.sidebar-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-12px);
  padding: 0 var(--space-8px);
}
.primary-nav {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}
.primary-nav :deep(nav) {
  display: flex;
  flex-direction: column;
  gap: var(--space-2px);
  overflow-y: auto;
  overscroll-behavior-y: contain;
  min-height: 0;
}
.nav-group {
  font-size: var(--font-13px);
  font-weight: 500;
  color: var(--t3);
  margin: var(--space-16px) var(--space-14px) var(--space-6px);
}
.nav-group:first-child {
  margin-top: var(--space-4px);
}
.primary-nav :deep(nav a) {
  display: flex;
  align-items: center;
  gap: var(--space-12px);
  min-height: 44px;
  padding: 0 var(--space-14px);
  border-radius: var(--radius-10px);
  color: var(--t1);
  font-size: var(--font-15px);
  font-weight: 500;
  white-space: nowrap;
  transition:
    background var(--motion-0d2s) linear,
    color var(--motion-0d2s) linear;
}
.primary-nav :deep(nav a svg) {
  transition: color var(--motion-0d2s) linear;
  color: var(--t2);
  flex-shrink: 0;
}
.primary-nav :deep(nav a:hover) {
  background: var(--hover);
}
.primary-nav :deep(nav a.router-link-active),
.primary-nav :deep(nav a.active) {
  background: var(--brand-soft);
  color: var(--brand);
  font-weight: 600;
}
.primary-nav :deep(nav a.router-link-active svg),
.primary-nav :deep(nav a.active svg) {
  color: var(--brand);
}
.primary-nav :deep(nav a:focus-visible) {
  outline-offset: -2px;
}
.sidebar-foot {
  margin-top: auto;
  padding-top: var(--space-12px);
}
.sidebar-foot :deep(.mirror-link) {
  display: flex;
  justify-content: center;
  padding: var(--space-14px) var(--space-8px);
  border-top: 1px solid var(--line);
  font-size: var(--font-13d5px);
}
.header-link {
  display: inline-flex;
  align-items: center;
  gap: var(--space-3px);
  color: var(--brand);
  font-weight: 600;
  white-space: nowrap;
}
.header-link:hover {
  text-decoration: underline;
  text-underline-offset: 3px;
}
.account {
  display: flex;
  align-items: center;
  gap: var(--space-12px);
  padding: var(--space-14px) var(--space-8px) 0;
  border-top: 1px solid var(--line);
  min-width: 0;
}
.avatar {
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--brand);
  color: var(--brand-ink);
  font-size: var(--font-16px);
  font-weight: 600;
}
.account-name {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}
.account-name :deep(strong) {
  font-size: var(--font-15px);
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.account-name :deep(small) {
  font-size: var(--font-12d5px);
}
.nav-scrim {
  display: none;
}
.main-shell {
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior-y: contain;
  scrollbar-gutter: stable;
}
.page-content {
  max-width: 1560px;
  padding: var(--space-44px) var(--space-48px) var(--space-72px) var(--space-60px);
}
@media (max-width: 1180px) {
  .page-content {
    padding: var(--space-36px) var(--space-32px) var(--space-56px);
  }
}
@media (max-width: 900px) {
  .app-shell {
    display: block;
    height: auto;
    overflow: visible;
  }
}
@media (max-width: 900px) {
  .main-shell {
    overflow: visible;
    scrollbar-gutter: auto;
  }
}
@media (max-width: 900px) {
  .sidebar {
    position: sticky;
    top: 0;
    flex-direction: row;
    align-items: center;
    height: var(--header-height);
    padding: 0 var(--space-12px) 0 var(--space-20px);
    border-right: 0;
    border-bottom: 1px solid var(--line);
  }
}
@media (max-width: 900px) {
  .sidebar-top {
    flex: 1;
    padding: 0;
  }
}
@media (max-width: 900px) {
  .primary-nav {
    position: fixed;
    top: var(--header-height);
    left: 0;
    right: 0;
    z-index: 39;
    max-height: calc(100vh - var(--header-height));
    overflow-y: auto;
    padding: var(--space-6px) var(--space-16px) var(--space-16px);
    background: var(--bg);
    border-bottom: 1px solid var(--line);
    visibility: hidden;
    opacity: 0;
    transform: translateY(-8px);
    transition:
      opacity var(--motion-0d15s),
      transform var(--motion-0d15s),
      visibility var(--motion-0s) var(--motion-0d15s);
  }
}
@media (max-width: 900px) {
  .primary-nav.open {
    visibility: visible;
    opacity: 1;
    transform: none;
    transition:
      opacity var(--motion-0d15s),
      transform var(--motion-0d15s);
  }
}
@media (max-width: 900px) {
  .primary-nav :deep(nav) {
    overflow: visible;
  }
}
@media (max-width: 900px) {
  .nav-scrim {
    display: block;
    position: fixed;
    inset: var(--header-height) 0 0;
    z-index: 38;
    background: var(--scrim);
  }
}
@media (max-width: 900px) {
  .page-content {
    padding: var(--space-32px) var(--space-24px) var(--space-56px);
  }
}
@media (max-width: 600px) {
  .sidebar {
    padding: 0 var(--space-8px) 0 var(--space-16px);
  }
}
@media (max-width: 600px) {
  .page-content {
    padding: var(--space-28px) var(--space-16px) var(--space-48px);
  }
}
</style>
