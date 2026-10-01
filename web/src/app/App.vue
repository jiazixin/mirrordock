<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { ArrowRight, ShieldCheck } from '@lucide/vue'
import Shell from '../layouts/Shell.vue'
import logo from '../assets/logo.svg'
import { api } from './api'
import { connectEvents, disconnectEvents, refresh, state } from './state'

const loading = ref(true)
const authenticated = ref(false)
const username = ref('admin')
const password = ref('')
const error = ref('')
const busy = ref(false)

async function check() {
  try {
    const status = await api<{ authenticated: boolean; username?: string }>('/auth/status')
    authenticated.value = status.authenticated
    state.username = status.username ?? ''
    if (status.authenticated) { await refresh(); connectEvents() }
  } catch (e) { error.value = e instanceof Error ? e.message : String(e) }
  finally { loading.value = false }
}
async function submit() {
  busy.value = true
  error.value = ''
  try {
    const session = await api<{ username: string }>('/auth/login', 'POST', { username: username.value, password: password.value })
    password.value = ''
    state.username = session.username
    authenticated.value = true
    await refresh()
    connectEvents()
  } catch (e) { error.value = e instanceof Error ? e.message : String(e) }
  finally { busy.value = false }
}
async function logout() {
  try { await api('/auth/logout', 'POST'); expired() }
  catch (e) { state.error = e instanceof Error ? e.message : String(e) }
}
function expired() {
  disconnectEvents()
  authenticated.value = false
  state.username = ''
  state.config = null
  state.runs = []
}
onMounted(() => { check(); window.addEventListener('session-expired', expired) })
onBeforeUnmount(() => { disconnectEvents(); window.removeEventListener('session-expired', expired) })
</script>

<template>
  <div v-if="loading" class="loading-screen"><img :src="logo" alt="MirrorDock"/><p>连接源仓…</p></div>
  <Shell v-else-if="authenticated" @logout="logout"/>
  <main v-else class="login-screen">
    <div class="login-art">
      <span class="eyebrow">KEEP YOUR SOURCES CLOSE</span>
      <h1>每一份软件，<br>都在你的掌控中<span>。</span></h1>
      <p>同步、记录、分发。<br>一个源仓，连接你的软件世界。</p>
      <div class="login-grid"><span>RSYNC</span><span>RELEASE</span><span>REPOSYNC</span><span>HTTP / TLS</span></div>
      <small>MirrorDock · 自托管软件源</small>
    </div>
    <section class="login-form">
      <div class="brand"><img :src="logo" alt=""/><div><strong>MirrorDock</strong><small>源仓</small></div></div>
      <h2>欢迎回来</h2>
      <p>登录以管理同步源与运行计划。</p>
      <form @submit.prevent="submit">
        <label>用户名<input v-model="username" required autocomplete="username" placeholder="请输入用户名"/></label>
        <label>管理员密码<input v-model="password" type="password" autocomplete="current-password" required placeholder="请输入密码"/></label>
        <div v-if="error" class="alert error" role="alert">{{error}}</div>
        <button class="button primary" :disabled="busy">{{busy ? '正在连接…' : '登录管理台'}}<ArrowRight :size="17"/></button>
      </form>
      <small><ShieldCheck :size="15"/>镜像目录公开读取 · 管理操作需要登录</small>
    </section>
  </main>
</template>
