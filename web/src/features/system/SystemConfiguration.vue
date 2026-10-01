<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { CheckCircle2, RotateCcw, Server, FolderOpen, ShieldCheck } from '@lucide/vue'
import { api } from '../../app/api'
import { state, perform, refresh, notify } from '../../app/state'
import type { SystemChecks } from '../../app/types'

const checks = ref<SystemChecks>()
const checking = ref(false)
const reloading = ref(false)
const deployment = computed(() => state.info?.deployment)
const platform = computed(() => ({ linux: 'Linux', darwin: 'macOS', windows: 'Windows' }[state.info?.os ?? ''] ?? state.info?.os ?? '—'))
const offset = computed(() => {
  const seconds = state.info?.utc_offset_seconds ?? 0
  const minutes = Math.floor(Math.abs(seconds) / 60)
  return `UTC${seconds >= 0 ? '+' : '−'}${String(Math.floor(minutes / 60)).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`
})
const runtimeRows = computed(() => [
  { label: '程序版本', value: `MirrorDock v${state.info?.version ?? '—'}` },
  { label: 'Go 版本', value: state.info?.go ?? '—' },
  { label: '操作系统 / 架构', value: `${platform.value} / ${state.info?.arch ?? '—'}` },
  { label: '逻辑 CPU', value: state.info?.cpus ? `${state.info.cpus} 核` : '—' },
  { label: '启动时区', value: `${state.info?.timezone ?? '—'} · ${offset.value}` },
  { label: '服务协议', value: state.info?.tls ? 'HTTPS · TLS 已启用' : 'HTTP' },
])
const pathRows = computed(() => [
  { label: '监听地址', value: deployment.value?.listen ?? '—' },
  { label: '数据目录', value: deployment.value?.data ?? '—' },
  { label: 'SQLite 数据库', value: deployment.value?.database ?? '—' },
  { label: '镜像根目录', value: deployment.value?.mirror_root ?? '—' },
  { label: '程序日志', value: deployment.value?.log.path || '标准输出' },
  { label: '日志级别 / 格式', value: `${deployment.value?.log.level ?? '—'} / ${deployment.value?.log.format ?? '—'}` },
  { label: 'TLS 证书链', value: deployment.value?.tls.cert || '未配置' },
  { label: 'TLS 私钥路径', value: deployment.value?.tls.key || '未配置' },
])
function toolVersion(tool: SystemChecks['rsync'] | undefined, name: 'rsync' | 'dnf') {
  if (!tool?.available) return name === 'dnf' ? '可选；未检测到可用的 DNF' : '未检测到可用的 rsync'
  const pattern = name === 'rsync' ? /\brsync\s+version\s+(\d+(?:\.\d+)+)/i : /^\s*(\d+(?:\.\d+)+)\s*$/m
  const version = tool.version.match(pattern)?.[1]
  return version ? `版本 ${version}` : '可执行，版本未识别'
}
const checkRows = computed(() => {
  const result = checks.value
  return [
    { label: 'rsync', status: result?.rsync.available ? '可用' : '不可用', ok: result?.rsync.available, detail: toolVersion(result?.rsync, 'rsync') },
    { label: 'DNF', status: result?.dnf.available ? '可用' : '不可用', ok: result?.dnf.available, detail: `${toolVersion(result?.dnf, 'dnf')}；未检查 reposync 插件` },
    { label: '配置目录', status: result?.config_writable ? '可写' : '不可写', ok: result?.config_writable, detail: 'YAML 原子写回需要在此目录创建临时文件' },
    { label: '镜像根目录', status: result?.mirror_writable ? '可写' : '不可写', ok: result?.mirror_writable, detail: '同步与发布使用此目录' },
    { label: 'GitHub Token', status: result?.github_token_configured ? '已配置' : '未配置', ok: result?.github_token_configured, detail: '仅检查配置状态，未验证 API 认证' },
    { label: '下载代理', status: result?.github_proxy_configured ? '已配置' : '直连', ok: true, detail: '仅检查配置状态，未测试网络连通性' },
  ]
})
async function check() {
  checking.value = true
  checks.value = undefined
  try { checks.value = await api<SystemChecks>('/system/checks') }
  finally { checking.value = false }
}
async function reload() {
  reloading.value = true
  try {
    await api('/config/reload', 'POST')
    await refresh()
    await check()
    notify('已重新载入磁盘配置')
  } finally { reloading.value = false }
}
onMounted(() => { void perform(check) })
</script>

<template>
  <section class="system-page">
    <div class="page-heading">
      <div><span class="eyebrow">SYSTEM CONFIGURATION</span><h1>系统配置<span>.</span></h1><p>查看当前配置文件、程序运行环境与目录权限。</p></div>
      <div class="heading-actions">
        <button class="button" :disabled="reloading || checking" @click="perform(reload)"><RotateCcw :size="16"/>{{ reloading ? '载入中…' : '重新载入配置' }}</button>
        <button class="button primary" :disabled="checking || reloading" @click="perform(check)"><CheckCircle2 :size="16"/>{{ checking ? '检查中…' : '检查运行环境' }}</button>
      </div>
    </div>
    <div v-if="state.config?.restart_required" class="alert warning">YAML 中的账号、日志、监听、目录或 TLS 已修改，请重启程序后生效。下方运行信息仍为当前进程实际使用的配置。</div>
    <article class="panel active-config">
      <span class="eyebrow">ACTIVE CONFIGURATION</span><h2>当前配置文件</h2>
      <code>{{ state.config?.file }}</code>
      <p>设置写回此 YAML 文件。监听、目录、账号、日志和 TLS 在配置文件中修改，重启后生效。</p>
    </article>
    <div class="system-grid">
      <article class="panel system-panel">
        <div class="system-panel-heading"><Server :size="19"/><h2>运行环境</h2></div>
        <dl><div v-for="row in runtimeRows" :key="row.label" class="detail-row"><dt>{{ row.label }}</dt><dd>{{ row.value }}</dd></div></dl>
      </article>
      <article class="panel system-panel">
        <div class="system-panel-heading"><FolderOpen :size="19"/><h2>监听与存储</h2></div>
        <dl><div v-for="row in pathRows" :key="row.label" class="detail-row"><dt>{{ row.label }}</dt><dd><code>{{ row.value }}</code></dd></div></dl>
      </article>
    </div>
    <article class="panel system-panel environment-checks" :aria-busy="checking">
      <div class="system-panel-heading"><ShieldCheck :size="19"/><h2>环境检查</h2><small>{{ checking ? '正在检查…' : checks ? '检查完成' : '尚未取得检查结果' }}</small></div>
      <p class="check-note">检查本机工具与写入权限；不执行同步。工具可执行不代表已通过实际发行版验收。</p>
      <div v-if="checks" class="checks-list">
        <div v-for="row in checkRows" :key="row.label" class="check-row">
          <strong>{{ row.label }}</strong><span class="check-status" :class="row.ok ? 'ready' : 'unavailable'">{{ row.status }}</span><p>{{ row.detail }}</p>
        </div>
      </div>
      <p v-else class="check-empty" role="status">{{ checking ? '正在读取工具版本和目录权限…' : '检查未完成，请点击“检查运行环境”重试。' }}</p>
    </article>
  </section>
</template>

<style scoped>
.system-page { display: flex; flex-direction: column; gap: 22px; }
.page-heading { margin-bottom: 8px; }
.active-config, .system-panel { padding: 26px 28px; }
.active-config h2, .system-panel h2 { font-size: 17px; }
.active-config code { display: block; margin: 14px 0; overflow-wrap: anywhere; color: var(--dark); font-size: 13px; }
.active-config p, .check-note, .check-empty { color: var(--muted); font-size: 13px; line-height: 1.8; }
.system-grid { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 22px; }
.system-panel-heading { display: flex; gap: 10px; align-items: center; margin-bottom: 20px; }
.system-panel-heading > svg { color: var(--blue); flex-shrink: 0; }
.system-panel-heading small { margin-left: auto; }
dl { margin: 0; }
.detail-row { display: grid; grid-template-columns: 125px minmax(0, 1fr); gap: 20px; padding: 14px 0; border-top: 1px solid var(--line); font-size: 13px; line-height: 1.7; }
dt { color: var(--muted); }
dd { margin: 0; overflow-wrap: anywhere; }
dd code { font-size: 12px; }
.check-note { margin-bottom: 18px; }
.check-row { display: grid; grid-template-columns: 130px 80px minmax(0, 1fr); align-items: center; gap: 22px; border-top: 1px solid var(--line); padding: 16px 0; font-size: 13px; }
.check-row strong { font-weight: 500; }
.check-row p { color: var(--muted); line-height: 1.7; }
.check-status { width: fit-content; padding: 4px 10px; border-radius: 6px; font-size: 12px; white-space: nowrap; }
.ready { color: #238569; background: #edf8f3; }
.unavailable { color: #946322; background: #fff6e7; }
@media (max-width: 1100px) { .system-grid { grid-template-columns: 1fr; } }
@media (max-width: 600px) {
  .active-config, .system-panel { padding: 22px 18px; }
  .detail-row { grid-template-columns: 105px minmax(0, 1fr); gap: 12px; }
  .check-row { grid-template-columns: minmax(0, 1fr) auto; gap: 10px; }
  .check-row p { grid-column: 1 / -1; }
}
</style>
