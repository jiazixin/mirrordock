<script setup lang="ts">
import { errorText } from './errors'
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { ArrowRight, ShieldCheck } from '@lucide/vue'
import AppShell from '../layouts/AppShell.vue'
import logo from '../assets/logo.svg'
import { api } from './api'
import { connectEvents, disconnectEvents, refresh, resetState, setUsername, notify } from './state'

const loading = ref(true)
const authenticated = ref(false)
const username = ref('admin')
const password = ref('')
const error = ref('')
const busy = ref(false)
let authRevision = 0

async function syncNotificationTimezone(revision: number) {
  try {
    await api<undefined>({
      scope: 'session',
      path: '/notifications/timezone',
      method: 'PUT',
      body: {
        timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      },
    })
  } catch {
    if (revision === authRevision) notify('通知时区同步失败，继续使用服务端已保存的时区。', 'error')
  }
}
async function check() {
  const revision = ++authRevision
  try {
    const status = await api<{ authenticated: boolean; username?: string }>({
      scope: 'session',
      path: '/auth/session',
    })
    if (revision !== authRevision) return
    authenticated.value = status.authenticated
    setUsername(status.username ?? '')
    if (status.authenticated) {
      await syncNotificationTimezone(revision)
      if (revision !== authRevision) return
      await refresh()
      if (revision === authRevision && authenticated.value) connectEvents()
    }
  } catch (e) {
    error.value = errorText(e)
  } finally {
    loading.value = false
  }
}
async function submit() {
  const revision = ++authRevision
  busy.value = true
  error.value = ''
  try {
    const session = await api<{ authenticated: boolean; username?: string }>({
      scope: 'session',
      path: '/auth/login',
      method: 'POST',
      body: {
        username: username.value,
        password: password.value,
      },
    })
    if (revision !== authRevision) return
    password.value = ''
    setUsername(session.username ?? '')
    authenticated.value = true
    await syncNotificationTimezone(revision)
    if (revision !== authRevision) return
    await refresh()
    // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- the compared state can change while awaiting
    if (revision === authRevision && authenticated.value) connectEvents()
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
async function logout() {
  try {
    await api<undefined>({ scope: 'session', path: '/auth/logout', method: 'POST' })
    expired()
  } catch (e) {
    notify(errorText(e), 'error')
  }
}
function expired() {
  authRevision++
  resetState()
  authenticated.value = false
}
onMounted(() => {
  void check()
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
  <AppShell
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

<style scoped>
.login-screen {
  min-height: 100vh;
  display: grid;
  grid-template-columns: minmax(0, 1.1fr) minmax(0, 1fr);
  background: var(--bg);
}
.login-art {
  padding: 12vh 10% 8vh;
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--tint-line);
  background: var(--tint);
}
.login-art :deep(.brand) {
  margin-bottom: auto;
}
.login-art :deep(h1) {
  font-size: var(--font-44px);
  font-weight: 700;
  line-height: 1.25;
  letter-spacing: -0.03em;
  margin-top: var(--space-64px);
  max-width: 560px;
}
.login-art :deep(p) {
  font-size: var(--font-15px);
  line-height: 1.8;
  color: var(--t2);
  margin-top: var(--space-20px);
  max-width: 440px;
}
.login-grid {
  display: grid;
  grid-template-columns: repeat(4, auto);
  justify-content: start;
  margin-top: var(--space-40px);
  gap: var(--space-8px);
}
.login-grid :deep(span) {
  font-size: var(--font-13px);
  font-weight: 500;
  color: var(--brand);
  background: var(--brand-soft);
  border-radius: var(--radius-7px);
  padding: var(--space-3px) var(--space-10px);
}
.login-art > :deep(small) {
  margin-top: auto;
  padding-top: var(--space-48px);
}
.login-form {
  width: 360px;
  max-width: 100%;
  margin: auto;
  padding: var(--space-48px) var(--space-8px);
}
.login-form :deep(.brand) {
  display: none;
  margin-bottom: var(--space-48px);
}
.login-form :deep(h2) {
  font-size: var(--font-28px);
  font-weight: 700;
  margin-bottom: var(--space-8px);
}
.login-form > :deep(p) {
  color: var(--t2);
  font-size: var(--font-13px);
}
.login-form :deep(form) {
  display: flex;
  flex-direction: column;
  gap: var(--space-20px);
  margin-top: var(--space-32px);
}
.login-form :deep(form input) {
  height: 44px;
}
.login-form :deep(form .button) {
  height: 46px;
  justify-content: space-between;
  margin-top: var(--space-4px);
  font-size: var(--font-15px);
}
.login-form :deep(form .button.primary) {
  background: var(--brand);
  border-color: var(--brand);
  color: var(--brand-ink);
}
.login-form > :deep(small) {
  display: flex;
  gap: var(--space-7px);
  align-items: center;
  margin-top: var(--space-28px);
}
.loading-screen {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-16px);
  font-size: var(--font-13px);
  color: var(--t3);
}
.loading-screen :deep(img) {
  width: 28px;
}
@media (max-width: 900px) {
  .login-screen {
    display: flex;
    padding: var(--space-20px);
  }
}
@media (max-width: 900px) {
  .login-art {
    display: none;
  }
}
@media (max-width: 900px) {
  .login-form :deep(.brand) {
    display: flex;
  }
}
</style>
