<script setup lang="ts">
import { ref, watch } from 'vue'
import { Save, ShieldCheck, Settings2, ArrowDownUp, FileText, Bell } from '@lucide/vue'
import { clone, state, saveConfiguration, reloadConfiguration, perform } from '../../app/state'
import type { Configuration } from '../../app/types'
import EnvironmentChecks from './EnvironmentChecks.vue'
import Notifications from './Notifications.vue'
import OptionCard from '../../components/OptionCard.vue'
import RefreshButton from '../../components/RefreshButton.vue'
import { vTabIndicator } from '../../lib/motion/tabIndicator'

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
const baseline = ref<Configuration>()
const draftRevision = ref('')
const conflict = ref(false)
function adoptLatest() {
  if (!state.config) return
  draft.value = clone(state.config.config)
  baseline.value = clone(state.config.config)
  draftRevision.value = state.config.revision
  token.value = ''
  clearToken.value = false
  conflict.value = false
}
watch(
  () => state.config?.revision,
  () => {
    if (!state.config || busy.value) return
    const dirty =
      draft.value &&
      baseline.value &&
      (JSON.stringify(draft.value.settings) !== JSON.stringify(baseline.value.settings) ||
        token.value ||
        clearToken.value)
    if (!dirty) {
      adoptLatest()
      return
    }
    if (JSON.stringify(state.config.config.settings) === JSON.stringify(baseline.value?.settings)) {
      // Notification/source-only changes can merge without touching settings drafts.
      const settings = draft.value!.settings
      draft.value = clone(state.config.config)
      draft.value.settings = settings
      baseline.value = clone(state.config.config)
      draftRevision.value = state.config.revision
    } else {
      conflict.value = true
    }
  },
  { immediate: true },
)

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
  if (!draft.value || conflict.value) return
  busy.value = true
  try {
    await saveConfiguration(
      draft.value,
      clearToken.value ? '' : token.value || undefined,
      draft.value.settings.github.proxy,
      draftRevision.value,
    )
    adoptLatest()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section
    v-if="draft"
    class="settings-page"
  >
    <div class="page-heading">
      <div>
        <h1>
          系统设置<span
            class="title-dot"
            aria-hidden="true"
            >.</span
          >
        </h1>
      </div>
      <div class="heading-actions">
        <RefreshButton
          class="button"
          :disabled="busy"
          :action="() => perform(reloadConfiguration)"
          >重新载入配置</RefreshButton
        >
        <button
          v-if="['execution', 'rsync', 'github'].includes(activeTab)"
          class="button primary"
          :disabled="busy || conflict"
          @click="perform(save)"
        >
          <Save :size="16" />{{ busy ? '保存中…' : '保存设置' }}
        </button>
      </div>
    </div>
    <div
      v-if="state.config?.restart_required"
      class="alert warning"
    >
      配置文件中的账号、日志、监听、目录或 TLS 已修改，请重启程序后生效。
    </div>
    <div
      v-if="conflict"
      class="alert warning"
      role="alert"
    >
      设置已在其他位置修改，未保存草稿已保留。请复制需要保留的内容，再载入最新设置后重新编辑。
      <button
        class="button"
        @click="adoptLatest"
      >
        载入最新设置（丢弃草稿）
      </button>
    </div>
    <div
      v-tab-indicator
      class="tabs settings-tabs"
      role="tablist"
      aria-label="系统设置分类"
    >
      <button
        v-for="(tab, index) in tabs"
        :id="`settings-tab-${tab.id}`"
        :key="tab.id"
        role="tab"
        :class="{ active: activeTab === tab.id }"
        :aria-selected="activeTab === tab.id"
        :aria-controls="`settings-panel-${tab.id}`"
        :tabindex="activeTab === tab.id ? 0 : -1"
        @click="activeTab = tab.id"
        @keydown="navigateTab($event, index)"
      >
        <component
          :is="tab.icon"
          :size="16"
        />{{ tab.label }}
      </button>
    </div>

    <div
      id="settings-panel-execution"
      v-show="activeTab === 'execution'"
      class="settings-sections"
      role="tabpanel"
      aria-labelledby="settings-tab-execution"
    >
      <article class="panel setting-row">
        <div class="setting-description">
          <span class="setting-index">01 · 执行预算</span>
          <h3>任务累计超时</h3>
          <p>设置一轮同步可使用的总执行时间。</p>
          <small
            >自动重试、切换来源与暂停后继续共享预算；排队和暂停不计时。修改对新任务生效。</small
          >
        </div>
        <div class="setting-controls">
          <label
            >任务累计超时<input
              v-model="draft.settings.task_timeout"
              placeholder="8h"
            /><small>支持 30m、8h 等时间长度，默认 8h。</small></label
          >
        </div>
      </article>
      <article class="panel setting-row">
        <div class="setting-description">
          <span class="setting-index">02 · 重试</span>
          <h3>失败自动重试</h3>
          <p>网络断开或上游临时不可用时自动再试。</p>
          <small
            >适用于 Rsync、Reposync 和 GitHub Release；Rsync 先尝试备用来源。等待从 5
            秒开始逐次翻倍，最多 80 秒，共享累计超时。磁盘、权限和配置错误直接停止。</small
          >
        </div>
        <div class="setting-controls">
          <label
            >失败重试次数<input
              v-model.number="draft.settings.retry_count"
              type="number"
              min="0"
              max="10"
              step="1"
            /><small>0–10 次，默认额外重试 3 次；0 关闭。修改对新任务生效。</small></label
          >
        </div>
      </article>
      <article class="panel setting-row">
        <div class="setting-description">
          <span class="setting-index">03 · 并发</span>
          <h3>同类型并发数</h3>
          <p>每种同步类型最多同时执行的任务数。</p>
          <small
            >各类型独立计数，Rsync 大仓库不会占用 GitHub Release
            的名额；超出的任务按触发顺序排队。保存后立即生效，已在执行的任务不受影响。</small
          >
        </div>
        <div class="setting-controls">
          <label
            >Rsync 并发数<input
              v-model.number="draft.settings.concurrency.rsync"
              type="number"
              min="1"
              max="16"
              step="1"
          /></label>
          <label
            >Reposync 并发数<input
              v-model.number="draft.settings.concurrency.reposync"
              type="number"
              min="1"
              max="16"
              step="1"
          /></label>
          <label
            >GitHub Release 并发数<input
              v-model.number="draft.settings.concurrency.github_release"
              type="number"
              min="1"
              max="16"
              step="1"
            /><small>每种 1–16 个，默认 4 个。</small></label
          >
        </div>
      </article>
    </div>

    <div
      id="settings-panel-rsync"
      v-show="activeTab === 'rsync'"
      class="settings-sections"
      role="tabpanel"
      aria-labelledby="settings-tab-rsync"
    >
      <article class="panel setting-row">
        <div class="setting-description">
          <span class="setting-index">01 · 连接</span>
          <h3>连接与 I/O 超时</h3>
          <p>避免上游连接或传输长时间无响应。</p>
          <small>I/O 超时表示无数据传输的等待期限，整轮执行仍受任务累计超时限制。</small>
        </div>
        <div class="setting-controls form-grid">
          <label
            >Rsync I/O 超时<input
              v-model="draft.settings.rsync.io_timeout"
              placeholder="5m"
            /><small>默认 5m。</small></label
          ><label
            >Rsync 连接超时<input
              v-model="draft.settings.rsync.connect_timeout"
              placeholder="30s"
            /><small>默认 30s。</small></label
          >
        </div>
      </article>
      <article class="panel setting-row">
        <div class="setting-description">
          <span class="setting-index">02 · 带宽</span>
          <h3>传输限速</h3>
          <p>控制每个 Rsync 上游任务的下载带宽。</p>
          <small>仅影响同步任务；公开镜像下载不受此限速影响。</small>
        </div>
        <div class="setting-controls">
          <label
            >Rsync 传输限速 · MB/s<input
              v-model.number="draft.settings.rsync.limit_mb"
              type="number"
              min="0"
              step="any"
            /><small>十进制 MB/s，可填小数；0 不限速。</small></label
          >
        </div>
      </article>
      <article class="panel setting-row">
        <div class="setting-description">
          <span class="setting-index">03 · 网络</span>
          <h3>下载代理</h3>
          <p>Rsync 来源默认经此代理连接上游。</p>
          <small>同步源可单独选择跟随全局、直连或自定义。</small>
          <small>HTTP(S) 代理须允许 CONNECT 到 rsync 端口。</small>
          <small>代理失败时本轮报错，不回退直连。</small>
        </div>
        <div class="setting-controls">
          <label
            >Rsync 代理 · proxy<input
              v-model="draft.settings.rsync.proxy"
              type="text"
              autocomplete="off"
              spellcheck="false"
              placeholder="socks5://127.0.0.1:1080"
            /><small>支持 http、https、socks5、socks5h，可带账号密码。</small></label
          >
        </div>
      </article>
    </div>

    <div
      id="settings-panel-github"
      v-show="activeTab === 'github'"
      class="settings-sections"
      role="tabpanel"
      aria-labelledby="settings-tab-github"
    >
      <article class="panel setting-row">
        <div class="setting-description">
          <span class="setting-index">01 · 版本范围</span>
          <h3>Release 下载范围</h3>
          <p>按发布时间选择最近的 Release。</p>
          <small>单个同步源可覆盖默认数量；仅下载匹配的附件，始终忽略草稿和自动源码归档。</small>
        </div>
        <div class="setting-controls">
          <label
            >默认最近 N 个 Release<input
              v-model.number="draft.settings.github.release_count"
              type="number"
              min="1"
              max="100"
            /><small>1–100 个，默认 5 个。</small></label
          >
          <div class="setting-option-grid">
            <OptionCard
              v-model="draft.settings.github.include_prereleases"
              label="包含预发布版本"
              description="将预发布版本纳入下载范围"
            />
            <OptionCard
              v-model="draft.settings.github.prune_old_releases"
              label="成功后清理超出数量的旧版本"
              description="同步成功后清理旧版本及保留版本内已移除的附件"
            />
          </div>
        </div>
      </article>
      <article class="panel setting-row">
        <div class="setting-description">
          <span class="setting-index">02 · 凭证</span>
          <h3>GitHub Token</h3>
          <p>为 GitHub API 请求提供认证。</p>
          <small
            >当前{{
              state.config?.token_configured ? '已配置' : '未配置'
            }}。留空保留现有值，保存后输入框清空；读取 API 不返回凭证。</small
          >
        </div>
        <div class="setting-controls">
          <label
            >GitHub Token<input
              v-model="token"
              type="password"
              autocomplete="new-password"
              :disabled="clearToken"
              :placeholder="
                state.config?.token_configured
                  ? '已配置，留空保留现有 Token'
                  : '未配置，可直接粘贴 Token'
              "
          /></label>
          <div class="setting-option-grid">
            <OptionCard
              v-model="clearToken"
              label="清空现有 Token"
              description="保存设置后移除已保存的 Token"
            />
          </div>
        </div>
      </article>
      <article class="panel setting-row">
        <div class="setting-description">
          <span class="setting-index">03 · 网络</span>
          <h3>下载代理</h3>
          <p>GitHub API 和附件下载共用此代理。</p>
        </div>
        <div class="setting-controls">
          <label
            >GitHub 代理 · proxy<input
              v-model="draft.settings.github.proxy"
              type="text"
              autocomplete="off"
              spellcheck="false"
              placeholder="http://127.0.0.1:8081"
            /><small
              >显示当前代理地址，可直接修改；留空并保存即直连。保存后对下一次执行生效。</small
            ></label
          >
        </div>
      </article>
    </div>

    <div
      v-if="activeTab === 'notifications'"
      id="settings-panel-notifications"
      role="tabpanel"
      aria-labelledby="settings-tab-notifications"
    >
      <Notifications />
    </div>

    <div
      id="settings-panel-configuration"
      v-if="activeTab === 'configuration'"
      class="settings-sections"
      role="tabpanel"
      aria-labelledby="settings-tab-configuration"
    >
      <article class="panel active-config">
        <h2>配置文件</h2>
        <code>{{ state.config?.file }}</code>
        <p>程序版本：{{ state.info?.version ?? '未知' }}</p>
      </article>
      <EnvironmentChecks />
    </div>
  </section>
</template>

<style scoped>
.settings-tabs {
  overflow-x: auto;
}
.active-config {
  padding: 8px 0 24px;
  border: 0;
  border-radius: 0;
  background: transparent;
}
.active-config h2 {
  font-size: 15px;
}
.active-config code {
  display: block;
  margin-top: 12px;
  overflow-wrap: anywhere;
  color: var(--t1);
  font-size: 13px;
}
.active-config p {
  margin-top: 8px;
  font-size: 13px;
  color: var(--t2);
}
.settings-sections {
  display: flex;
  flex-direction: column;
  padding: 0 26px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--panel);
}
.setting-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 40px;
  padding: 28px 0;
  border: 0;
  border-top: 1px solid var(--line);
  border-radius: 0;
  background: transparent;
  overflow: visible;
}
.setting-row:first-child {
  border-top: 0;
}
.setting-description {
  max-width: 520px;
}
.setting-index {
  display: block;
  font-size: 12px;
  color: var(--t3);
  margin-bottom: 10px;
}
.setting-description h3 {
  font-size: 16px;
  margin-bottom: 8px;
}
.setting-description p {
  color: var(--t2);
  font-size: 13px;
  line-height: 1.7;
  margin-bottom: 10px;
}
.setting-description small {
  display: block;
}
.setting-row small {
  word-break: keep-all;
  overflow-wrap: anywhere;
}
.setting-controls {
  display: flex;
  flex-direction: column;
  gap: 18px;
  min-width: 0;
}
.setting-controls.form-grid {
  display: grid;
  align-content: start;
}
.setting-option-grid {
  display: grid;
  gap: 12px;
}
@media (max-width: 900px) {
  .setting-row {
    grid-template-columns: 1fr;
    gap: 22px;
  }
}
@media (max-width: 480px) {
  .settings-tabs {
    gap: 18px;
  }
  .settings-sections {
    padding: 0 18px;
  }
}
</style>
