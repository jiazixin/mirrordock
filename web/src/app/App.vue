<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { ArrowRight, ShieldCheck } from '@lucide/vue'
import Shell from '../layouts/Shell.vue'
import logo from '../assets/logo.svg'
import { api } from './api'
import { connectEvents, disconnectEvents, refresh, resetState, state, notify } from './state'

const loading = ref(true)
const authenticated = ref(false)
const username = ref('admin')
const password = ref('')
const error = ref('')
const busy = ref(false)
let authRevision = 0

async function syncNotificationTimezone(revision: number) {
  try {
    await api('/notifications/timezone', 'POST', {
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    })
  } catch {
    if (revision === authRevision) notify('通知时区同步失败，继续使用服务端已保存的时区。', 'error')
  }
}
async function check() {
  const revision = ++authRevision
  try {
    const status = await api<{ authenticated: boolean; username?: string }>('/auth/status')
    if (revision !== authRevision) return
    authenticated.value = status.authenticated
    state.username = status.username ?? ''
    if (status.authenticated) {
      await syncNotificationTimezone(revision)
      if (revision !== authRevision) return
      await refresh()
      if (revision === authRevision && authenticated.value) connectEvents()
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}
async function submit() {
  const revision = ++authRevision
  busy.value = true
  error.value = ''
  try {
    const session = await api<{ username: string }>('/auth/login', 'POST', {
      username: username.value,
      password: password.value,
    })
    if (revision !== authRevision) return
    password.value = ''
    state.username = session.username
    authenticated.value = true
    await syncNotificationTimezone(revision)
    if (revision !== authRevision) return
    await refresh()
    if (revision === authRevision && authenticated.value) connectEvents()
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    busy.value = false
  }
}
async function logout() {
  try {
    await api('/auth/logout', 'POST')
    expired()
  } catch (e) {
    notify(e instanceof Error ? e.message : String(e), 'error')
  }
}
function expired() {
  authRevision++
  resetState()
  authenticated.value = false
}
onMounted(() => {
  check()
  window.addEventListener('session-expired', expired)
})
onBeforeUnmount(() => {
  disconnectEvents()
  window.removeEventListener('session-expired', expired)
})
</script>

<template>
  <div
    v-if="loading"
    class="loading-screen"
  >
    <img
      :src="logo"
      alt="MirrorDock"
    />
    <p>正在连接…</p>
  </div>
  <Shell
    v-else-if="authenticated"
    @logout="logout"
  />
  <main
    v-else
    class="login-screen"
  >
    <div class="login-art">
      <div class="brand">
        <img
          :src="logo"
          alt=""
        /><strong>MirrorDock</strong>
      </div>
      <h1>软件源同步<br />与镜像分发</h1>
      <p>按计划从上游同步，校验后发布不可变的镜像代；失败时保留上一代，公开下载不受影响。</p>
      <div class="login-grid">
        <span>Rsync</span><span>Reposync</span><span>GitHub Release</span><span>HTTP / TLS</span>
      </div>
      <small>源仓 · 自托管软件源</small>
    </div>
    <section class="login-form">
      <div class="brand">
        <img
          :src="logo"
          alt=""
        /><strong>MirrorDock</strong>
      </div>
      <h2>登录管理台</h2>
      <p>管理同步源、运行计划与通知。</p>
      <form @submit.prevent="submit">
        <label
          >用户名<input
            v-model="username"
            required
            autocomplete="username"
            placeholder="请输入用户名"
        /></label>
        <label
          >管理员密码<input
            v-model="password"
            type="password"
            autocomplete="current-password"
            required
            placeholder="请输入密码"
        /></label>
        <div
          v-if="error"
          class="alert error"
          role="alert"
        >
          {{ error }}
        </div>
        <button
          class="button primary"
          :disabled="busy"
        >
          {{ busy ? '正在连接…' : '登录管理台' }}<ArrowRight :size="16" />
        </button>
      </form>
      <small><ShieldCheck :size="14" />镜像目录公开读取 · 管理操作需要登录</small>
    </section>
  </main>
</template>
