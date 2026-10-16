<script setup lang="ts">
import { useRoute } from 'vue-router'
import { LogOut, Menu, X, ArrowUpRight, Monitor, Moon, Sun } from '@lucide/vue'
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import ToastStack from '../components/ToastStack.vue'
import { state, dismissToast } from '../app/state'
import { theme, themeLabel, cycleTheme } from '../app/theme'
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
const links = [
  { path: '/overview', label: '概览' },
  { path: '/sources', label: '同步源' },
  { path: '/batches', label: '批次记录' },
  { path: '/records', label: '运行记录' },
  { path: '/schedules', label: '计划管理' },
  { path: '/analytics/rsync', label: '数据分析', section: '/analytics/' },
  { path: '/settings', label: '系统设置' },
]
const themeIcon = computed(() =>
  theme.value === 'dark' ? Moon : theme.value === 'light' ? Sun : Monitor,
)
</script>

<template>
  <ToastStack
    :items="state.toasts"
    @dismiss="dismissToast"
  />
  <div class="app-shell">
    <header class="app-header">
      <RouterLink
        class="brand"
        to="/overview"
        ><img
          :src="logo"
          alt=""
        /><strong>MirrorDock</strong></RouterLink
      >
      <nav
        id="primary-navigation"
        class="primary-nav"
        :class="{ open: mobile }"
        aria-label="主导航"
      >
        <RouterLink
          v-for="link in links"
          :key="link.path"
          :to="link.path"
          :class="{ active: link.section && route.path.startsWith(link.section) }"
          :aria-current="
            (link.section ? route.path.startsWith(link.section) : route.path === link.path)
              ? 'page'
              : undefined
          "
          @click="mobile = false"
          >{{ link.label }}</RouterLink
        >
      </nav>
      <div class="header-tools">
        <span
          class="live-status"
          :class="{ on: state.connected }"
          :title="state.connected ? '状态实时推送中' : '实时连接已断开，正在重连'"
          >{{ state.connected ? '实时' : '未连接' }}</span
        >
        <a
          class="header-link"
          href="/"
          target="_blank"
          rel="noopener"
          >镜像目录<ArrowUpRight :size="14"
        /></a>
        <button
          class="icon-button"
          type="button"
          :aria-label="`界面主题：${themeLabel}，点击切换`"
          :title="`界面主题：${themeLabel}`"
          @click="cycleTheme"
        >
          <component
            :is="themeIcon"
            :size="17"
          />
        </button>
        <button
          class="account"
          type="button"
          aria-label="退出登录"
          :title="`${state.username} · 退出登录`"
          @click="emit('logout')"
        >
          <strong>{{ state.username }}</strong
          ><LogOut :size="15" />
        </button>
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
            :size="19"
          />
        </button>
      </div>
    </header>
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
