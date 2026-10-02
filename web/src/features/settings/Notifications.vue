<script setup lang="ts">
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { CircleHelp, Save, Send, RotateCcw } from '@lucide/vue'
import Pagination from '../../components/Pagination.vue'
import OptionCard from '../../components/OptionCard.vue'
import { api } from '../../app/api'
import { state, notify } from '../../app/state'
import type { ConfigView } from '../../app/types'

interface Channel {
  kind: string
  enabled: boolean
  only_failure: boolean
  has_endpoint: boolean
  has_token: boolean
  has_secret: boolean
  chat_id: string
  ready: boolean
}
interface View {
  channels: Channel[]
  revision: string
  timezone: string
}
interface Draft {
  enabled: boolean
  only_failure: boolean
  endpoint: string
  token: string
  secret: string
  chat_id: string
  clear_secret: boolean
}
interface DeliveryRecord {
  id: number
  run_id: number
  channel: string
  state: string
  attempts: number
  updated_at: string
  error: string
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
const saved = ref<Record<string, Channel>>({})
const drafts = ref<Record<string, Draft>>({})
const busy = ref<Record<string, 'save' | 'test' | undefined>>({})
const revision = ref('')
const timezone = ref('UTC')
const records = ref<DeliveryRecord[]>([])
const page = ref(1),
  pageSize = ref(50),
  total = ref(0)
let recordRevision = 0
const error = ref('')
const recordError = ref('')
const refreshing = ref(false)
const help = ref('')
const pinned = ref('')
let timer: ReturnType<typeof setInterval> | undefined
let disposed = false
const zoneFormatter = computed(() => {
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      timeZone: timezone.value,
      dateStyle: 'short',
      timeStyle: 'medium',
    })
  } catch {
    return new Intl.DateTimeFormat('zh-CN', {
      timeZone: 'UTC',
      dateStyle: 'short',
      timeStyle: 'medium',
    })
  }
})
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
  try {
    const view = await api<View>('/notifications')
    if (!disposed) updateView(view, true)
  } catch (e) {
    if (!disposed) error.value = e instanceof Error ? e.message : '通知设置读取失败'
  }
}
async function refreshRecords() {
  const request = ++recordRevision
  refreshing.value = true
  try {
    const result = await api<{ items: DeliveryRecord[]; total: number }>(
      `/notifications/records?limit=${pageSize.value}&offset=${(page.value - 1) * pageSize.value}`,
    )
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
    if (!disposed && request === recordRevision)
      recordError.value = e instanceof Error ? e.message : '发送记录读取失败'
  } finally {
    if (request === recordRevision) refreshing.value = false
  }
}
async function act(kind: string, operation: 'save' | 'test') {
  if (busy.value[kind]) return
  busy.value[kind] = operation
  try {
    if (operation === 'save') {
      const result = await api<View>(`/notifications/${kind}`, 'PUT', {
        ...drafts.value[kind],
        revision: revision.value,
      })
      if (!disposed) {
        updateView(result)
        const channel = result.channels.find((item) => item.kind === kind)!
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
      const config = await api<ConfigView>('/config')
      if (!disposed) state.config = config
    } else {
      await api(`/notifications/${kind}/test`, 'POST')
      if (!disposed) notify(`${channelName(kind)}测试通知发送成功`)
    }
    await refreshRecords()
  } catch (e) {
    if (!disposed)
      notify(`${channelName(kind)}：${e instanceof Error ? e.message : '操作失败'}`, 'error')
  } finally {
    delete busy.value[kind]
  }
}
const labels: Record<string, string> = {
  waiting: '等待发送',
  sending: '正在发送',
  sent: '已发送',
  failed: '发送失败',
  stopped: '已停止',
}
function channelName(kind: string) {
  return definitions.find((item) => item.kind === kind)?.name ?? kind
}
function recordTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '未记录' : zoneFormatter.value.format(date)
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
      {{ error }}
    </div>
    <p
      v-if="!Object.keys(saved).length && !error"
      role="status"
    >
      正在读取通知设置…
    </p>
    <article
      v-for="channel in definitions.filter((item) => drafts[item.kind])"
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
              v-model="drafts[channel.kind]!.token"
              type="password"
              autocomplete="new-password"
              :disabled="!!busy[channel.kind]"
              :placeholder="
                saved[channel.kind]?.has_token ? '已保存，留空保持不变' : '123456:ABC…'
              "
          /></label>
          <label
            >Chat ID<input
              v-model="drafts[channel.kind]!.chat_id"
              autocomplete="off"
              :disabled="!!busy[channel.kind]"
              placeholder="用户或群组 ID，群组保留负号"
          /></label>
        </template>
        <label v-else
          >{{ channel.kind === 'bark' ? '服务地址 / 设备 Key' : '推送地址'
          }}<input
            v-model="drafts[channel.kind]!.endpoint"
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
              v-model="drafts[channel.kind]!.secret"
              type="password"
              autocomplete="new-password"
              :disabled="!!busy[channel.kind] || drafts[channel.kind]!.clear_secret"
              :placeholder="saved[channel.kind]?.has_secret ? '已保存，留空保持不变' : 'SEC…'"
          /></label>
        </template>
        <div class="channel-option-grid">
          <OptionCard
            v-model="drafts[channel.kind]!.enabled"
            label="启用"
            description="自动发送此后新建任务的结果"
            :disabled="!!busy[channel.kind]"
          />
          <OptionCard
            v-model="drafts[channel.kind]!.only_failure"
            label="仅失败时通知"
            description="仅发送含失败结果的任务通知"
            :disabled="!!busy[channel.kind]"
          />
          <OptionCard
            v-if="channel.kind === 'dingtalk'"
            v-model="drafts[channel.kind]!.clear_secret"
            label="清除已保存的加签密钥"
            description="保存设置后移除已保存的密钥"
            :disabled="!!busy[channel.kind]"
            @update:model-value="drafts[channel.kind]!.secret = ''"
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
        <button
          class="button"
          :disabled="refreshing"
          @click="refreshRecords"
        >
          <RotateCcw :size="16" />{{ refreshing ? '刷新中…' : '刷新' }}
        </button>
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
                <td class="explanation">{{ record.error || '—' }}</td>
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
        <Pagination
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
  gap: 20px;
}
.notification-settings > .alert {
  background: var(--blue-light);
  border-color: #dce4fc;
  color: #53637c;
  margin-bottom: 0;
}
.notification-intro h2,
.records-heading h2 {
  font-size: 17px;
}
.notification-intro p,
.timezone-note,
.channel-description p {
  color: var(--muted);
  font-size: 12px;
  line-height: 1.8;
}
.notification-intro p {
  margin-top: 10px;
}
.notification-card {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  padding: 26px;
  gap: 36px;
  overflow: visible;
}
.channel-title {
  display: flex;
  align-items: center;
  gap: 10px;
}
.channel-title h3 {
  font-size: 16px;
}
.channel-description p {
  margin-top: 12px;
}
.channel-controls {
  display: flex;
  flex-direction: column;
  gap: 18px;
  min-width: 0;
}
.channel-controls label {
  font-size: 13px;
}
.channel-option-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}
.channel-actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  border-top: 1px solid var(--line);
  padding-top: 18px;
  margin-top: 8px;
}
.help-wrapper {
  position: relative;
  display: inline-flex;
}
.help-button {
  display: inline-flex;
  padding: 5px;
  color: var(--muted);
  border: 0;
  background: transparent;
  cursor: pointer;
}
.help-button:focus-visible {
  outline: 2px solid var(--blue);
  outline-offset: 2px;
  border-radius: 4px;
}
.help-popover {
  position: absolute;
  z-index: 20;
  top: 100%;
  left: -80px;
  width: min(340px, calc(100vw - 100px));
  padding: 15px;
  background: var(--paper, white);
  color: var(--dark);
  border: 1px solid var(--line);
  border-radius: 8px;
  box-shadow: 0 8px 24px #0002;
  font-size: 12px;
  line-height: 1.8;
  overflow-wrap: anywhere;
}
.records-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin: 12px 0 18px;
}
.notification-records > small {
  display: block;
  margin-top: 14px;
}
.notification-records td,
.notification-records th {
  text-align: center;
  white-space: nowrap;
}
.notification-records .explanation {
  text-align: left;
  white-space: normal;
  min-width: 180px;
  max-width: 360px;
}
.notification-records .notification-empty {
  white-space: normal;
  padding: 36px 20px;
  color: var(--muted);
}
.delivery-state {
  display: inline-block;
  padding: 4px 7px;
  border-radius: 4px;
  background: #f7f9fc;
  font-size: 12px;
}
.delivery-state.sent {
  color: #16805c;
  background: #edf8f2;
}
.delivery-state.failed {
  color: #b44242;
  background: #fdf0f0;
}
@media (max-width: 900px) {
  .notification-card {
    grid-template-columns: 1fr;
    gap: 24px;
    padding: 24px;
  }
}
@media (max-width: 480px) {
  .notification-card {
    padding: 22px 18px;
  }
  .channel-option-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
