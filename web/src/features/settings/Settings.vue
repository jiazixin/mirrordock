<script setup lang="ts">
import { ref, watch } from 'vue'
import { Save, RotateCcw, ShieldCheck, Settings2, ArrowDownUp, FileText, Bell } from '@lucide/vue'
import { clone, state, saveConfiguration, perform, refresh, notify } from '../../app/state'
import type { Configuration } from '../../app/types'
import { api } from '../../app/api'
import EnvironmentChecks from './EnvironmentChecks.vue'
import Notifications from './Notifications.vue'

const tabs = [
  { id: 'rsync', label: 'Rsync', icon: ArrowDownUp },
  { id: 'github', label: 'GitHub Release', icon: ShieldCheck },
  { id: 'execution', label: '同步策略', icon: Settings2 },
  { id: 'configuration', label: '系统配置', icon: FileText },
  { id: 'notifications', label: '通知设置', icon: Bell },
] as const
const activeTab = ref<(typeof tabs)[number]['id']>('rsync')
const draft = ref<Configuration>()
const token = ref('')
const clearToken = ref(false)
const busy = ref(false)
watch(() => state.config?.revision, () => {
  if (state.config) draft.value = clone(state.config.config)
}, { immediate: true })

function navigateTab(event: KeyboardEvent, index: number) {
  let next = index
  if (event.key === 'ArrowRight') next = (index + 1) % tabs.length
  else if (event.key === 'ArrowLeft') next = (index + tabs.length - 1) % tabs.length
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = tabs.length - 1
  else return
  event.preventDefault()
  activeTab.value = tabs[next]!.id
  const button = event.currentTarget as HTMLButtonElement
  button.parentElement?.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus()
}
async function save() {
  if (!draft.value) return
  busy.value = true
  try {
    await saveConfiguration(draft.value, clearToken.value ? '' : token.value || undefined, draft.value.settings.github.proxy)
    token.value = ''; clearToken.value = false
  } finally { busy.value = false }
}
async function reload() {
  await api('/config/reload', 'POST'); await refresh(); notify('已重新载入磁盘配置')
}
</script>

<template>
  <section v-if="draft" class="settings-page">
    <div class="page-heading">
      <div><span class="eyebrow">SYSTEM PREFERENCES</span><h1>系统设置<span>.</span></h1></div>
      <div class="heading-actions">
        <button class="button" :disabled="busy" @click="perform(reload)"><RotateCcw :size="16"/>重新载入配置</button>
        <button v-if="['execution', 'rsync', 'github'].includes(activeTab)" class="button primary" :disabled="busy" @click="perform(save)"><Save :size="16"/>{{ busy ? '保存中…' : '保存设置' }}</button>
      </div>
    </div>
    <div v-if="state.config?.restart_required" class="alert warning">配置文件中的账号、日志、监听、目录或 TLS 已修改，请重启程序后生效。</div>
    <div class="tabs settings-tabs" role="tablist" aria-label="系统设置分类">
      <button v-for="(tab, index) in tabs" :id="`settings-tab-${tab.id}`" :key="tab.id" role="tab"
        :class="{ active: activeTab === tab.id }" :aria-selected="activeTab === tab.id"
        :aria-controls="`settings-panel-${tab.id}`" :tabindex="activeTab === tab.id ? 0 : -1"
        @click="activeTab = tab.id" @keydown="navigateTab($event, index)">
        <component :is="tab.icon" :size="16"/>{{ tab.label }}
      </button>
    </div>

    <div id="settings-panel-execution" v-show="activeTab === 'execution'" class="settings-sections" role="tabpanel" aria-labelledby="settings-tab-execution">
      <article class="panel setting-row">
        <div class="setting-description"><span class="setting-index">01 · 执行预算</span><h3>任务累计超时</h3><p>设置一轮同步可使用的总执行时间。</p><small>自动重试、切换来源与暂停后继续共享预算；排队和暂停不计时。修改对新任务生效。</small></div>
        <div class="setting-controls"><label>任务累计超时<input v-model="draft.settings.task_timeout" placeholder="8h"/><small>支持 30m、8h 等时间长度，默认 8h。</small></label></div>
      </article>
      <article class="panel setting-row">
        <div class="setting-description"><span class="setting-index">02 · 重试</span><h3>失败自动重试</h3><p>网络断开或上游临时不可用时自动再试。</p><small>适用于 Rsync、Reposync 和 GitHub Release；Rsync 先尝试备用来源。等待从 5 秒开始逐次翻倍，最多 80 秒，共享累计超时。磁盘、权限和配置错误直接停止。</small></div>
        <div class="setting-controls"><label>失败重试次数<input v-model.number="draft.settings.retry_count" type="number" min="0" max="10" step="1"/><small>0–10 次，默认额外重试 3 次；0 关闭。修改对新任务生效。</small></label></div>
      </article>
    </div>

    <div id="settings-panel-rsync" v-show="activeTab === 'rsync'" class="settings-sections" role="tabpanel" aria-labelledby="settings-tab-rsync">
      <article class="panel setting-row">
        <div class="setting-description"><span class="setting-index">01 · 连接</span><h3>连接与 I/O 超时</h3><p>避免上游连接或传输长时间无响应。</p><small>I/O 超时表示无数据传输的等待期限，整轮执行仍受任务累计超时限制。</small></div>
        <div class="setting-controls form-grid"><label>Rsync I/O 超时<input v-model="draft.settings.rsync.io_timeout" placeholder="5m"/><small>默认 5m。</small></label><label>Rsync 连接超时<input v-model="draft.settings.rsync.connect_timeout" placeholder="30s"/><small>默认 30s。</small></label></div>
      </article>
      <article class="panel setting-row">
        <div class="setting-description"><span class="setting-index">02 · 带宽</span><h3>传输限速</h3><p>控制每个 Rsync 上游任务的下载带宽。</p><small>仅影响同步任务；公开镜像下载不受此限速影响。</small></div>
        <div class="setting-controls"><label>Rsync 传输限速 · MB/s<input v-model.number="draft.settings.rsync.limit_mb" type="number" min="0" step="any"/><small>十进制 MB/s，可填小数；0 不限速。</small></label></div>
      </article>
    </div>

    <div id="settings-panel-github" v-show="activeTab === 'github'" class="settings-sections" role="tabpanel" aria-labelledby="settings-tab-github">
      <article class="panel setting-row">
        <div class="setting-description"><span class="setting-index">01 · 版本范围</span><h3>Release 下载范围</h3><p>按发布时间选择最近的 Release。</p><small>单个同步源可覆盖默认数量；仅下载匹配的附件，始终忽略草稿和自动源码归档。</small></div>
        <div class="setting-controls"><label>默认最近 N 个 Release<input v-model.number="draft.settings.github.release_count" type="number" min="1" max="100"/><small>1–100 个，默认 5 个。</small></label><label class="check"><input v-model="draft.settings.github.include_prereleases" type="checkbox"/>包含预发布版本</label><label class="check"><input v-model="draft.settings.github.prune_old_releases" type="checkbox"/>成功后清理超出数量的旧版本</label></div>
      </article>
      <article class="panel setting-row">
        <div class="setting-description"><span class="setting-index">02 · 凭证</span><h3>GitHub Token</h3><p>为 GitHub API 请求提供认证。</p><small>当前{{ state.config?.token_configured ? '已配置' : '未配置' }}。留空保留现有值，保存后输入框清空；读取 API 不返回凭证。</small></div>
        <div class="setting-controls"><label>GitHub Token<input v-model="token" type="password" autocomplete="new-password" :disabled="clearToken" :placeholder="state.config?.token_configured ? '已配置，留空保留现有 Token' : '未配置，可直接粘贴 Token'"/></label><label class="check"><input v-model="clearToken" type="checkbox"/>清空现有 Token</label></div>
      </article>
      <article class="panel setting-row">
        <div class="setting-description"><span class="setting-index">03 · 网络</span><h3>下载代理</h3><p>GitHub API 和附件下载共用此代理。</p><small>需要正向代理入口；http:// 用于 HTTP/mixed，https:// 要求代理入口本身支持 TLS，SOCKS 使用 socks5:// 或 socks5h://。未配置时直连，不读取系统代理。</small></div>
        <div class="setting-controls"><label>GitHub 代理 · proxy<input v-model="draft.settings.github.proxy" type="text" autocomplete="off" spellcheck="false" placeholder="http://127.0.0.1:8081"/><small>显示当前代理地址，可直接修改；留空并保存即直连。保存后对下一次执行生效。</small></label></div>
      </article>
    </div>

    <div v-if="activeTab === 'notifications'" id="settings-panel-notifications" role="tabpanel" aria-labelledby="settings-tab-notifications"><Notifications /></div>

    <div id="settings-panel-configuration" v-if="activeTab === 'configuration'" class="settings-sections" role="tabpanel" aria-labelledby="settings-tab-configuration">
      <article class="panel active-config">
        <h2>配置文件</h2>
        <code>{{ state.config?.file }}</code>
      </article>
      <EnvironmentChecks />
    </div>
  </section>
</template>

<style scoped>
.settings-tabs { gap: 24px; overflow-x: auto; }
.active-config { padding: 26px 28px; }
.active-config h2 { font-size: 17px; }
.active-config code { display: block; margin-top: 14px; overflow-wrap: anywhere; color: var(--dark); font-size: 13px; }
.settings-tabs button { white-space: nowrap; }
.settings-sections { display: flex; flex-direction: column; gap: 20px; }
.setting-row { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 36px; padding: 26px; }
.setting-description { max-width: 530px; }
.setting-index { display: block; font-size: 11px; font-weight: 500; letter-spacing: .6px; color: var(--muted); margin-bottom: 10px; }
.setting-description h3 { font-size: 16px; margin-bottom: 10px; }
.setting-description p { color: var(--muted); font-size: 12px; line-height: 1.8; margin-bottom: 12px; }
.setting-description small { display: block; }
.setting-controls { display: flex; flex-direction: column; gap: 18px; min-width: 0; }
.setting-controls.form-grid { display: grid; align-content: start; }
.setting-controls label { font-size: 13px; }
.setting-controls input:not([type=checkbox]) { max-width: none; }
.setting-controls .check { font-weight: 400; color: var(--dark); }
@media (max-width: 1150px) { .setting-row { gap: 28px; padding: 24px; } }
@media (max-width: 900px) { .setting-row { grid-template-columns: 1fr; gap: 24px; } .setting-controls input:not([type=checkbox]) { max-width: none; } }
@media (max-width: 480px) { .settings-tabs { gap: 16px; } .settings-tabs button { font-size: 12px; gap: 5px; } .setting-row { padding: 22px 18px; } }
</style>
