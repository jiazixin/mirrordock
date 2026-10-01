<script setup lang="ts">
import { useRoute } from 'vue-router'
import { FolderSync, Layers3, ScrollText, Clock3, Settings2, MonitorCog, LogOut, Menu, ExternalLink, ArrowUpRight } from '@lucide/vue'
import { ref } from 'vue'
import { state } from '../app/state'
import logo from '../assets/logo.svg'
const emit = defineEmits<{ logout: [] }>()
const route = useRoute()
const mobile = ref(false)
const links = [{ path: '/sources', label: '同步源', icon: FolderSync, group: '工作台' }, { path: '/batches', label: '批次记录', icon: Layers3, group: '任务记录' }, { path: '/records', label: '运行记录', icon: ScrollText }, { path: '/schedules', label: '计划管理', icon: Clock3, group: '运行管理' }, { path: '/settings', label: '系统设置', icon: Settings2, group: '系统' }, { path: '/system', label: '系统配置', icon: MonitorCog }]
</script>

<template>
  <div class="app-shell">
    <aside id="sidebar-navigation" class="sidebar" :class="{open:mobile}">
      <RouterLink class="brand" to="/sources"><img :src="logo" alt=""/><div><strong>MirrorDock</strong><small>源仓 · 软件镜像服务</small></div></RouterLink>
      <nav>
        <template v-for="link in links" :key="link.path">
          <span v-if="link.group" class="nav-group">{{ link.group }}</span>
          <RouterLink :to="link.path" @click="mobile=false"><component :is="link.icon" :size="19"/><span>{{ link.label }}</span><ArrowUpRight v-if="route.path===link.path" class="nav-arrow" :size="14"/></RouterLink>
        </template>
      </nav>
      <div class="sidebar-bottom">
        <a href="/" target="_blank" rel="noopener"><ExternalLink :size="16"/>浏览镜像目录</a>
        <div class="connection"><i :class="{offline:!state.connected}"></i>{{state.connected?'实时连接正常':'正在恢复连接'}}<small>v{{state.info?.version}}</small></div>
        <button class="account" aria-label="退出登录" @click="emit('logout')"><span class="avatar">{{Array.from(state.username)[0]?.toUpperCase()}}</span><span><strong>{{state.username}}</strong><small>管理员</small></span><LogOut :size="17"/></button>
      </div>
    </aside>
    <div v-if="mobile" class="sidebar-scrim" @click="mobile=false"></div>
    <main class="main-shell">
      <button class="icon-button mobile-menu" aria-label="打开导航" aria-controls="sidebar-navigation" :aria-expanded="mobile" @click="mobile=!mobile"><Menu :size="20"/></button>
      <div class="page-content">
        <div v-if="state.error" class="alert error" role="alert">{{state.error}}<button aria-label="关闭错误" @click="state.error=''">×</button></div>
        <div v-if="state.notice" class="alert success" role="status">{{state.notice}}</div>
        <RouterView />
      </div>
    </main>
  </div>
</template>
