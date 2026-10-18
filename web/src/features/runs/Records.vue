<script setup lang="ts">
import SelectField from '../../components/SelectField.vue'
import { computed, ref } from 'vue'
import Pagination from '../../components/Pagination.vue'
import RefreshButton from '../../components/RefreshButton.vue'
import ModalTransition from '../../components/ModalTransition.vue'
import { vTabIndicator } from '../../lib/motion/tabIndicator'
import { useRunPage } from './useRunPage'
import { timeRangeOptions } from './timeRanges'
import { CheckCircle2, CircleAlert, Search, Trash2, Info } from '@lucide/vue'
import StatusBadge from '../../components/StatusBadge.vue'
import RunDetail from './RunDetail.vue'
import { api } from '../../app/api'
import { state, perform, notify, confirmAction } from '../../app/state'
import StackedTime from '../../components/StackedTime.vue'
import { time, duration, bytes, sourceType } from '../../app/format'
const tab = ref('succeeded')
const source = ref('')
const age = ref('')
const search = ref('')
const detail = ref<number>()
const { page, pageSize, total, records, loading, load } = useRunPage({ state: tab, source, age })
const visible = computed(() =>
  records.value.filter((item) =>
    `${item.name} ${item.error_code} ${item.error}`
      .toLowerCase()
      .includes(search.value.toLowerCase()),
  ),
)
// 列表只展示首行主原因，完整错误见悬停提示与详情。
function mainReason(error: string) {
  return (
    error
      .split('\n')
      .map((line) => line.trim())
      .find(Boolean) ?? ''
  )
}
async function clear() {
  const accepted = await confirmAction({
    title: '清理日志',
    message: '清理已结束任务的原始日志？运行记录、来源尝试与错误摘要会保留。',
    confirmLabel: '清理',
    tone: 'red',
  })
  if (!accepted) return
  const payload = await api<{ cleared: number }>('/logs/clear', 'POST')
  notify(`已清理 ${payload.cleared} 份日志`)
  await load()
}
</script>
<template>
  <section>
    <div class="page-heading">
      <div>
        <h1>
          运行记录<span
            class="title-dot"
            aria-hidden="true"
            >.</span
          >
        </h1>
      </div>
      <div class="heading-actions">
        <button
          class="button tone-amber"
          @click="perform(clear)"
        >
          <Trash2 :size="16" />清理日志</button
        ><RefreshButton
          class="button"
          :action="() => perform(load)"
          >刷新</RefreshButton
        >
      </div>
    </div>
    <div
      v-tab-indicator
      class="tabs"
    >
      <button
        :class="{ active: tab === 'succeeded' }"
        @click="tab = 'succeeded'"
      >
        <CheckCircle2 :size="17" />成功记录</button
      ><button
        :class="{ active: tab === 'failure' }"
        @click="tab = 'failure'"
      >
        <CircleAlert :size="17" />失败记录
      </button>
    </div>
    <div class="panel">
      <div class="filter-toolbar">
        <label class="search-field"
          ><Search :size="17" /><input
            v-model="search"
            placeholder="搜索当前页名称或错误…"
            aria-label="搜索当前页记录" /></label
        ><SelectField
          v-model="source"
          aria-label="按同步源筛选"
          :options="[
            { value: '', label: '全部同步源' },
            ...(state.config?.config.sources ?? []).map((item) => ({
              value: item.id,
              label: item.name,
            })),
          ]"
        /><SelectField
          v-model="age"
          aria-label="按时间筛选"
          :options="timeRangeOptions"
        /><span class="filter-count">共 {{ total }} 项</span>
      </div>
      <div class="table-scroll">
        <table class="records-table">
          <thead>
            <tr>
              <th class="center">时间</th>
              <th class="center">同步源</th>
              <th class="center">状态</th>
              <th class="center">耗时</th>
              <th class="center">数据量</th>
              <th class="center">
                {{ tab === 'succeeded' ? '处理结果' : '失败原因' }}
              </th>
              <th class="center">详情</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="run in visible"
              :key="run.id"
            >
              <td class="center">
                <StackedTime
                  :value="time(run.created_at)"
                  :datetime="run.created_at"
                />
              </td>
              <td class="center">
                <strong>{{ run.name }}</strong
                ><small>{{ sourceType(run.type) }} · #{{ run.id }}</small>
              </td>
              <td class="center"><StatusBadge :state="run.state" /></td>
              <td class="center">
                <strong>{{ duration(run.elapsed_seconds) }}</strong>
              </td>
              <td class="center">{{ bytes(run.progress.bytes) }}</td>
              <td class="record-reason center">
                <template v-if="run.error_code"
                  ><strong>{{ run.error_code }}</strong>
                  <p :title="run.error">{{ mainReason(run.error) }}</p></template
                ><template v-else
                  ><span
                    >成功发布<template
                      v-if="run.type === 'github_release' || run.progress.success > 0"
                    >
                      · {{ run.progress.success }} 项</template
                    ></span
                  ><span v-if="run.progress.skipped"
                    >复用 {{ run.progress.skipped }} 项</span
                  ></template
                >
              </td>
              <td class="center">
                <button
                  class="icon-button outlined"
                  aria-label="查看记录详情"
                  title="查看详情"
                  @click="detail = run.id"
                >
                  <Info :size="15" />
                </button>
              </td>
            </tr>
            <tr v-if="!visible.length">
              <td
                colspan="7"
                class="empty-state"
              >
                <component
                  :is="tab === 'succeeded' ? CheckCircle2 : CircleAlert"
                  :size="32"
                />
                <h3>暂无{{ tab === 'succeeded' ? '成功' : '失败' }}记录</h3>
                <p>任务结束后，记录会自动保存在这里。</p>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <Pagination
        v-model:page="page"
        v-model:page-size="pageSize"
        :total="total"
        :count="visible.length"
        :disabled="loading"
      />
    </div>
    <ModalTransition>
      <RunDetail
        v-if="detail"
        :id="detail"
        @close="detail = undefined"
      />
    </ModalTransition>
  </section>
</template>
