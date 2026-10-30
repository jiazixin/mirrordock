<script setup lang="ts">
import { formatServerTime } from '../../lib/format/date'
import FieldIssue from '../../components/FieldIssue.vue'
import { onScopeDispose } from 'vue'
import { useQueryState } from '../../app/use-query-state'
import { errorText } from '../../app/errors'
import { recordErrorText } from '../../app/error-text'
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { CircleHelp, Save, Send } from '@lucide/vue'
import PaginationBar from '../../components/PaginationBar.vue'
import RefreshButton from '../../components/RefreshButton.vue'
import OptionCard from '../../components/OptionCard.vue'
import { api, taggedApi } from '../../app/api'
import { state, setConfiguration, notify } from '../../app/state'
import type { ConfigView } from '../../app/types'

type Channel = {
  kind: string
  enabled: boolean
  only_failure: boolean
  has_endpoint: boolean
  has_token: boolean
  has_secret: boolean
  chat_id: string
  ready: boolean
}
type View = {
  channels: Channel[]
  revision: string // from the ETag
  timezone: string
}
type Draft = {
  enabled: boolean
  only_failure: boolean
  endpoint: string
  token: string
  secret: string
  chat_id: string
  clear_secret: boolean
}
type DeliveryRecord = {
  id: number
  run_id: number
  channel: string
  state: string
  attempts: number
  updated_at: string
  error_code?: string
  error_details?: Record<string, unknown>
}
const definitions = [
  {
    kind: 'wecom',
    name: '企业微信',
    description: '企业微信群机器人 Webhook，包含 key。',
    help: '在企业微信群中添加群机器人，复制机器人提供的完整 Webhook 地址：https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=…，不要只填写 key。先保存，再发送测试。',
  },
  {
    kind: 'dingtalk',
    name: '钉钉',
    description: '钉钉群机器人 Webhook，支持可选加签。',
    help: '在钉钉群设置 → 智能群助手中添加自定义机器人，复制 https://oapi.dingtalk.com/robot/send?access_token=… 完整地址。如启用“加签”，另填机器人安全设置中 SEC 开头的密钥；关键词规则需允许 MirrorDock，IP 规则需允许服务器出口。先保存，再发送测试。',
  },
  {
    kind: 'telegram',
    name: 'Telegram',
    description: '填写 BotFather 创建的 Bot Token 和接收者 Chat ID。',
    help: '在 Telegram 联系 @BotFather，使用 /newbot 创建机器人并复制 Token。先向机器人发起会话；群通知需将机器人加入群组。通过 Bot API getUpdates 获取消息中的 chat.id，群组 ID 保留负号。填写 Token 与 Chat ID，先保存，再发送测试。',
  },
  {
    kind: 'bark',
    name: 'Bark',
    description: '填写以设备 Key 结尾的地址，支持自建服务和路径前缀。',
    help: '在 iOS Bark 应用复制推送地址，只保留“服务地址/设备 Key”，例如 https://api.day.app/设备Key/。删除设备 Key 后面的示例标题和正文；自建服务可填写 https://example.com/bark/设备Key/，末尾斜杠可保留。先保存，再发送测试。',
  },
  {
    kind: 'webhook',
    name: '通用 Webhook',
    description: '向接收地址 POST JSON，包含 event、title 和 body。',
    help: '在自己的接收服务创建 HTTP/HTTPS 接口，复制完整地址。接口需接收 event、title、body 三个 JSON 字段，并用 HTTP 2xx 表示成功；任务事件为 batch.finished，测试为 notification.test。先保存，再发送测试。',
  },
]
const readyChannels = computed(() =>
  definitions.flatMap((channel) => {
    const draft = drafts.value[channel.kind]
    return draft ? [{ ...channel, draft }] : []
  }),
)
const saved = ref<Record<string, Channel>>({})
const drafts = ref<Record<string, Draft>>({})
const validationError = ref<unknown>()
const mutationController = new AbortController()
onScopeDispose(() => {
  mutationController.abort()
})
const busy = ref<Record<string, 'save' | 'test' | undefined>>({})
const revision = ref('')
const timezone = ref('UTC')
const records = ref<DeliveryRecord[]>([])
const page = ref(1),
  pageSize = ref(50),
  total = ref(0)
useQueryState({ notificationPage: page, notificationPageSize: pageSize })
let recordRevision = 0
const error = ref('')
const recordError = ref('')
const refreshing = ref(false)
const help = ref('')
const pinned = ref('')
let timer: ReturnType<typeof setInterval> | undefined
let disposed = false
let settingsController: AbortController | undefined
let recordsController: AbortController | undefined
function closeHelp() {
  help.value = ''
  pinned.value = ''
}
function leaveHelp(kind: string) {
  if (pinned.value !== kind) help.value = ''
}
function toggleHelp(kind: string) {
  if (pinned.value === kind) closeHelp()
  else {
    pinned.value = kind
    help.value = kind
  }
}
function updateView(view: View, initialize = false) {
  revision.value = view.revision
  timezone.value = view.timezone
  for (const channel of view.channels) {
    saved.value[channel.kind] = channel
    if (initialize)
      drafts.value[channel.kind] = {
        enabled: channel.enabled,
        only_failure: channel.only_failure,
        endpoint: '',
        token: '',
        secret: '',
        chat_id: channel.chat_id,
        clear_secret: false,
      }
  }
}
async function load() {
  settingsController?.abort()
  const controller = new AbortController()
  settingsController = controller
  try {
    error.value = ''
    const view = await taggedApi<View>({ path: '/notifications', signal: controller.signal })
    if (!disposed && !controller.signal.aborted)
      updateView({ ...view.data, revision: view.etag }, true)
  } catch (e) {
    if (!disposed && !controller.signal.aborted) error.value = errorText(e)
  }
}
async function refreshRecords() {
  if (disposed) return
  recordsController?.abort()
  const controller = new AbortController()
  recordsController = controller
  const request = ++recordRevision
  refreshing.value = true
  try {
    const result = await api<{ items: DeliveryRecord[]; total: number }>({
      path: `/notifications/records?limit=${pageSize.value}&offset=${(page.value - 1) * pageSize.value}`,
      signal: controller.signal,
    })
    // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- the compared state can change while awaiting
    if (!disposed && request === recordRevision) {
      total.value = result.total
      const last = Math.max(1, Math.ceil(result.total / pageSize.value))
      if (page.value > last) {
        page.value = last
        return
      }
      records.value = result.items
      recordError.value = ''
    }
  } catch (e) {
    // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- the compared state can change while awaiting
    if (!disposed && request === recordRevision && !controller.signal.aborted)
      recordError.value = errorText(e)
  } finally {
    if (request === recordRevision) refreshing.value = false
  }
}
async function act(kind: string, operation: 'save' | 'test') {
  if (busy.value[kind]) return
  validationError.value = undefined
  busy.value[kind] = operation
  try {
    if (operation === 'save') {
      const tagged = await taggedApi<View>({
        path: `/notifications/${kind}`,
        method: 'PATCH',
        signal: mutationController.signal,
        headers: { 'If-Match': revision.value },
        body: channelPatch(kind, drafts.value[kind]),
      })
      const result = { ...tagged.data, revision: tagged.etag }
      if (!disposed) {
        updateView(result)
        const channel = result.channels.find((item) => item.kind === kind)
        if (channel)
          drafts.value[kind] = {
            enabled: channel.enabled,
            only_failure: channel.only_failure,
            endpoint: '',
            token: '',
            secret: '',
            chat_id: channel.chat_id,
            clear_secret: false,
          }
        notify(`${channelName(kind)}设置已保存`)
      }
      const config = await taggedApi<ConfigView>({
        path: '/config',
        signal: mutationController.signal,
      })
      if (!disposed) setConfiguration({ ...config.data, revision: config.etag })
    } else {
      await api<undefined>({
        path: `/notifications/${kind}/test`,
        method: 'POST',
        signal: mutationController.signal,
      })
      if (!disposed) notify(`${channelName(kind)}测试通知发送成功`)
    }
    await refreshRecords()
  } catch (e) {
    validationError.value = e
    if (!disposed) notify(`${channelName(kind)}：${errorText(e)}`, 'error')
  } finally {
    busy.value = Object.fromEntries(Object.entries(busy.value).filter(([key]) => key !== kind))
  }
}
/**
 * Returns the JSON Merge Patch of a channel draft: empty credential fields keep
 * the stored value, and a cleared secret is sent as null.
 */
function channelPatch(kind: string, draft: Draft | undefined): Record<string, unknown> {
  if (!draft) return {}
  const patch: Record<string, unknown> = {
    enabled: draft.enabled,
    only_failure: draft.only_failure,
  }
  if (draft.endpoint) patch.endpoint = draft.endpoint
  if (draft.token) patch.token = draft.token
  if (draft.secret) patch.secret = draft.secret
  else if (draft.clear_secret) patch.secret = null
  if (kind === 'telegram') patch.chat_id = draft.chat_id
  return patch
}
const labels: Record<string, string> = {
  waiting: '等待发送',
  sending: '正在发送',
  sent: '已发送',
  failed: '发送失败',
  stopped: '已停止',
}
function channelName(kind: string) {
  return definitions.find((item) => item.kind === kind)?.name ?? '未知渠道'
}
function recordTime(value: string) {
  return formatServerTime(value, { timezone: timezone.value, utcOffsetSeconds: 0 })
}
watch(
  pageSize,
  () => {
    page.value = 1
  },
  { flush: 'sync' },
)
watch([page, pageSize], () => {
  records.value = []
  void refreshRecords()
})
watch(
  () => state.config?.revision,
  (next) => {
    if (next && revision.value && next !== revision.value) void load()
  },
)
onMounted(() => {
  void load()
  void refreshRecords()
  timer = setInterval(() => {
    if (!refreshing.value) void refreshRecords()
  }, 5000)
})
onBeforeUnmount(() => {
  disposed = true
  settingsController?.abort()
  recordsController?.abort()
  clearInterval(timer)
})
</script>

<template>
  <div class="notification-settings">
    <div class="notification-intro">
      <h2>同步结束后，及时收到结果</h2>
      <p>每个渠道独立保存配置，先保存，再发送测试。</p>
    </div>
    <div class="alert">
      开启或重新开启后，仅通知此后新创建的任务。包括完成、部分失败、失败、取消与服务中断；失败最多尝试
      3 次，不影响同步结果。
    </div>
    <p class="timezone-note">
      通知使用最近登录浏览器的时区：{{
        timezone
      }}。关闭页面或重启服务后仍生效，已生成的正文保持原值。
    </p>
    <div
      v-if="error"
      class="alert error"
      role="alert"
    >
      {{ error
      }}<button
        class="button compact"
        @click="load"
      >
        重试
      </button>
    </div>
    <p
      v-if="!Object.keys(saved).length && !error"
      role="status"
    >
      正在读取通知设置…
    </p>
    <article
      v-for="channel in readyChannels"
      :key="channel.kind"
      class="panel notification-card"
      :data-channel="channel.kind"
    >
      <div class="channel-description">
        <div class="channel-title">
          <h3>{{ channel.name }}</h3>
          <span
            class="help-wrapper"
            @mouseenter="help = channel.kind"
            @mouseleave="leaveHelp(channel.kind)"
            @keydown.esc.stop.prevent="closeHelp"
          >
            <button
              class="help-button"
              type="button"
              :aria-label="`${channel.name}配置帮助`"
              :aria-expanded="help === channel.kind"
              :aria-controls="`help-${channel.kind}`"
              @focus="help = channel.kind"
              @blur="leaveHelp(channel.kind)"
              @click="toggleHelp(channel.kind)"
            >
              <CircleHelp :size="16" />
            </button>
            <span
              v-if="help === channel.kind"
              :id="`help-${channel.kind}`"
              class="help-popover"
              role="note"
              >{{ channel.help }}</span
            >
          </span>
        </div>
        <p>{{ channel.description }}</p>
      </div>
      <form
        class="channel-controls"
        @submit.prevent="act(channel.kind, 'save')"
      >
        <template v-if="channel.kind === 'telegram'">
          <label
            >Bot Token<input
              v-model="channel.draft.token"
              type="password"
              autocomplete="new-password"
              :disabled="!!busy[channel.kind]"
              :placeholder="
                saved[channel.kind]?.has_token ? '已保存，留空保持不变' : '123456:ABC…'
              " /><FieldIssue
              :error="validationError"
              field="token"
          /></label>
          <label
            >Chat ID<input
              v-model="channel.draft.chat_id"
              autocomplete="off"
              :disabled="!!busy[channel.kind]"
              placeholder="用户或群组 ID，群组保留负号" /><FieldIssue
              :error="validationError"
              field="chat_id"
          /></label>
        </template>
        <label v-else
          >{{ channel.kind === 'bark' ? '服务地址 / 设备 Key' : '推送地址'
          }}<input
            v-model="channel.draft.endpoint"
            type="password"
            autocomplete="new-password"
            :disabled="!!busy[channel.kind]"
            :placeholder="saved[channel.kind]?.has_endpoint ? '已保存，留空保持不变' : 'https://…'"
          /><small v-if="channel.kind === 'bark'"
            >删除设备 Key 后面的示例标题、正文；允许末尾斜杠。</small
          ></label
        >
        <template v-if="channel.kind === 'dingtalk'">
          <label
            >加签密钥（可选）<input
              v-model="channel.draft.secret"
              type="password"
              autocomplete="new-password"
              :disabled="!!busy[channel.kind] || channel.draft.clear_secret"
              :placeholder="
                saved[channel.kind]?.has_secret ? '已保存，留空保持不变' : 'SEC…'
              " /><FieldIssue
              :error="validationError"
              field="endpoint"
          /></label>
        </template>
        <div class="channel-option-grid">
          <OptionCard
            v-model="channel.draft.enabled"
            label="启用"
            description="自动发送此后新建任务的结果"
            :disabled="!!busy[channel.kind]"
          />
          <OptionCard
            v-model="channel.draft.only_failure"
            label="仅失败时通知"
            description="仅发送含失败结果的任务通知"
            :disabled="!!busy[channel.kind]"
          />
          <OptionCard
            v-if="channel.kind === 'dingtalk'"
            v-model="channel.draft.clear_secret"
            label="清除已保存的加签密钥"
            description="保存设置后移除已保存的密钥"
            :disabled="!!busy[channel.kind]"
            @update:model-value="channel.draft.secret = ''"
          />
        </div>
        <small
          >包含部分失败、失败、服务中断和含失败条目的任务；无失败的完成或取消不发送。留空的敏感字段保留已保存值。</small
        >
        <div class="channel-actions">
          <button
            class="button primary"
            :disabled="!!busy[channel.kind]"
            type="submit"
          >
            <Save :size="16" />{{ busy[channel.kind] === 'save' ? '保存中…' : '保存设置' }}</button
          ><button
            class="button"
            :disabled="!!busy[channel.kind] || !saved[channel.kind]?.ready"
            type="button"
            @click="act(channel.kind, 'test')"
          >
            <Send :size="16" />{{ busy[channel.kind] === 'test' ? '发送中…' : '发送测试' }}
          </button>
        </div>
      </form>
    </article>
    <section class="notification-records">
      <div class="records-heading">
        <h2>发送记录</h2>
        <RefreshButton
          class="button"
          :disabled="refreshing"
          :action="refreshRecords"
          >{{ refreshing ? '刷新中…' : '刷新' }}</RefreshButton
        >
      </div>
      <div
        v-if="recordError"
        class="alert error"
        role="alert"
      >
        {{ recordError }}
      </div>
      <div class="panel">
        <div class="table-scroll">
          <table>
            <thead>
              <tr>
                <th>任务编号</th>
                <th>渠道</th>
                <th>状态</th>
                <th>尝试次数</th>
                <th>更新时间</th>
                <th class="explanation">说明</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="record in records"
                :key="record.id"
              >
                <td>#{{ record.run_id }}</td>
                <td>{{ channelName(record.channel) }}</td>
                <td>
                  <span
                    class="delivery-state"
                    :class="record.state"
                    >{{ labels[record.state] ?? record.state }}</span
                  >
                </td>
                <td>{{ record.attempts }} / 3</td>
                <td>{{ recordTime(record.updated_at) }}</td>
                <td class="explanation">{{ recordErrorText(record) || '—' }}</td>
              </tr>
              <tr v-if="!records.length">
                <td
                  colspan="6"
                  class="notification-empty"
                >
                  暂无发送记录。保存并启用渠道后，新任务结束时会在这里显示结果。
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <PaginationBar
          v-model:page="page"
          v-model:page-size="pageSize"
          :total="total"
          :count="records.length"
          :disabled="refreshing"
        />
      </div>
      <small
        >默认每页 50 条，每 5 秒自动刷新。响应丢失或重启恢复可能重复送达，可用任务编号识别。</small
      >
    </section>
  </div>
</template>

<style scoped>
.notification-settings {
  display: flex;
  flex-direction: column;
  gap: var(--space-16px);
}
.notification-settings > .alert {
  margin-bottom: 0;
}
.notification-intro h2,
.records-heading h2 {
  font-size: var(--font-18px);
}
.notification-intro p,
.timezone-note,
.channel-description p {
  color: var(--t2);
  font-size: var(--font-13px);
  line-height: 1.7;
}
.notification-intro p {
  margin-top: var(--space-8px);
}
.timezone-note {
  color: var(--t3);
  font-size: var(--font-12px);
}
.notification-card {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: var(--space-40px);
  padding: var(--space-28px) 0;
  border: 0;
  border-top: 1px solid var(--line);
  border-radius: 0;
  background: transparent;
  overflow: visible;
}
.channel-title {
  display: flex;
  align-items: center;
  gap: var(--space-6px);
}
.channel-title h3 {
  font-size: var(--font-16px);
}
.channel-description p {
  margin-top: var(--space-8px);
}
.channel-controls {
  display: flex;
  flex-direction: column;
  gap: var(--space-16px);
  min-width: 0;
}
.channel-option-grid {
  display: grid;
  gap: var(--space-12px);
}
.channel-actions {
  display: flex;
  gap: var(--space-8px);
  flex-wrap: wrap;
  padding-top: var(--space-4px);
}
.help-wrapper {
  position: relative;
  display: inline-flex;
}
.help-button {
  display: inline-flex;
  padding: var(--space-5px);
  color: var(--t3);
  border: 0;
  background: transparent;
  cursor: pointer;
}
.help-button:hover {
  color: var(--t1);
}
.help-button:focus-visible {
  outline: 2px solid var(--focus);
  outline-offset: 2px;
  border-radius: var(--radius-4px);
}
.help-popover {
  position: absolute;
  z-index: 20;
  top: 100%;
  left: -80px;
  width: min(340px, calc(100vw - 100px));
  padding: var(--space-14px) var(--space-16px);
  background: var(--raised);
  color: var(--t1);
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-6px);
  box-shadow: var(--shadow);
  font-size: var(--font-12px);
  line-height: 1.8;
  overflow-wrap: anywhere;
}
.records-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin: var(--space-28px) 0 var(--space-16px);
}
.notification-records > small {
  display: block;
  margin-top: var(--space-12px);
}
.notification-records td,
.notification-records th {
  white-space: nowrap;
}
.notification-records .explanation {
  white-space: normal;
  min-width: 180px;
  max-width: 360px;
  color: var(--t2);
}
.notification-records .notification-empty {
  white-space: normal;
  padding: var(--space-36px) var(--space-20px);
  color: var(--t3);
  text-align: center;
}
.delivery-state {
  display: inline-flex;
  align-items: center;
  gap: var(--space-7px);
  font-size: var(--font-13px);
  color: var(--t2);
}
.delivery-state::before {
  content: '';
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--t3);
}
.delivery-state.sending::before,
.delivery-state.waiting::before {
  background: transparent;
  box-shadow: inset 0 0 0 1.5px var(--t3);
}
.delivery-state.failed {
  color: var(--red);
}
.delivery-state.failed::before {
  background: var(--red);
}
@media (max-width: 900px) {
  .notification-card {
    grid-template-columns: 1fr;
    gap: var(--space-22px);
  }
}
</style>
