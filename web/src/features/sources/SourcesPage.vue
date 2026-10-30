<script setup lang="ts">
import { useQueryState } from '../../app/use-query-state'
import type { DeepReadonly } from 'vue'
import PaginationBar from '../../components/PaginationBar.vue'
import RefreshButton from '../../components/RefreshButton.vue'
import ModalTransition from '../../components/ModalTransition.vue'
import { usePagination } from '../../lib/pagination/usePagination'
import SelectField from '../../components/SelectField.vue'
import { computed, ref } from 'vue'
import { Plus, Search, Play, Pencil, Trash2, FolderSync } from '@lucide/vue'
import SourceEditor from './SourceEditor.vue'
import StatusBadge from '../../components/StatusBadge.vue'
import {
  clone,
  confirmAction,
  state,
  perform,
  refresh,
  runSource,
  saveConfiguration,
} from '../../app/state'
import type { Source } from '../../app/types'
import StackedTime from '../../components/StackedTime.vue'
import { sourceType } from '../../app/labels'
import { time } from '../../app/server-time'
import { formatSize } from '../../lib/format/bytes'
const search = ref('')
const type = ref('')
const selected = ref('')
const edit = computed(() => {
  const source = state.config?.config.sources.find((item) => item.id === selected.value)
  return source ? clone(source) : undefined
})
const showEditor = computed(() => selected.value === '@new' || edit.value !== undefined)
const sources = computed(
  () =>
    state.config?.config.sources.filter(
      (source) =>
        (!type.value || source.type === type.value) &&
        `${source.name} ${source.id} ${source.dest}`
          .toLowerCase()
          .includes(search.value.toLowerCase()),
    ) ?? [],
)
const { page, pageSize, visible } = usePagination(sources, [search, type])
useQueryState(
  { search, type, page, pageSize, edit: selected },
  { type: ['', 'rsync', 'reposync', 'github_release'] },
)
const active = computed(
  () => state.runs.filter((run) => ['running', 'queued', 'pausing'].includes(run.state)).length,
)
function latest(id: string) {
  return state.runs.find((run) => run.source_id === id)
}
function editSource(source?: DeepReadonly<Source>) {
  selected.value = source?.id ?? '@new'
}
async function remove(source: DeepReadonly<Source>) {
  const accepted = await confirmAction({
    title: '删除同步源',
    message: `删除“${source.name}”？已保存的文件与运行记录会保留。`,
    confirmLabel: '删除',
    tone: 'red',
  })
  if (!accepted) return
  if (!state.config) return
  const config = clone(state.config.config)
  config.sources = config.sources.filter((item) => item.id !== source.id)
  await saveConfiguration({ config })
}
const typeOptions = [
  { value: '', label: '同步方式' },
  { value: 'rsync', label: 'Rsync' },
  { value: 'reposync', label: 'Reposync' },
  { value: 'github_release', label: 'GitHub Release' },
]
function sourceAddress(source: DeepReadonly<Source>) {
  return source.type === 'github_release' ? source.github_release.repository : source.upstreams.src1
}
</script>
<template>
  <section>
    <div class="page-heading">
      <div>
        <h1>
          同步源<span
            class="title-dot"
            aria-hidden="true"
            >.</span
          >
        </h1>
      </div>
      <div class="heading-actions">
        <RefreshButton
          class="button"
          :action="() => perform(refresh)"
          >刷新</RefreshButton
        ><button
          class="button primary"
          @click="editSource()"
        >
          <Plus :size="17" />新增同步源
        </button>
      </div>
    </div>
    <div class="stat-grid">
      <article>
        <span>同步源</span
        ><strong>{{ state.config?.config.sources.length ?? 0 }}<small>个</small></strong
        ><small
          >{{
            state.config?.config.sources.filter((item) => item.enabled).length ?? 0
          }}
          个启用</small
        >
      </article>
      <article>
        <span>正在运行</span><strong>{{ active }}<small>个任务</small></strong
        ><small>含排队与正在暂停</small>
      </article>
      <article>
        <span>已发布镜像</span
        ><strong>{{ state.info?.publications.length ?? 0 }}<small>个</small></strong
        ><small>公开路径 /&lt;dest&gt;/</small>
      </article>
      <article>
        <span>本次启动分发</span
        ><strong class="bytes-stat">{{ formatSize(state.info?.downloads.sent_bytes ?? 0) }}</strong
        ><small>{{ state.info?.downloads.active ?? 0 }} 条活动下载</small>
      </article>
    </div>
    <div class="panel">
      <div class="filter-toolbar">
        <label class="search-field"
          ><Search :size="17" /><input
            v-model="search"
            placeholder="搜索名称、标识或目录…"
            aria-label="搜索同步源"
        /></label>
        <div class="sync-method-filter">
          <SelectField
            v-model="type"
            aria-label="筛选同步方式"
            :options="typeOptions"
          />
        </div>
        <span class="filter-count">{{ sources.length }} 个源</span>
      </div>
      <div class="table-scroll">
        <table class="source-table">
          <thead>
            <tr>
              <th>同步源</th>
              <th>来源</th>
              <th>目标</th>
              <th class="center">最近运行</th>
              <th class="center">状态</th>
              <th class="center">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="source in visible"
              :key="source.id"
            >
              <td>
                <div class="source-name">
                  <div>
                    <strong>{{ source.name }}</strong
                    ><small
                      >{{ source.id
                      }}<span
                        class="enabled-dot"
                        :class="{ disabled: !source.enabled }"
                        >{{ source.enabled ? '已启用' : '已禁用' }}</span
                      ></small
                    >
                  </div>
                </div>
                <span
                  class="type-pill"
                  :class="source.type"
                  >{{ sourceType(source.type) }}</span
                >
              </td>
              <td>
                <div class="source-location">
                  <code
                    class="source-origin"
                    :title="sourceAddress(source)"
                    >{{ sourceAddress(source) }}</code
                  ><span
                    v-if="Object.keys(source.upstreams).length > 1"
                    class="backup-count"
                    >{{ Object.keys(source.upstreams).length - 1 }} 个备用来源</span
                  >
                </div>
              </td>
              <td>
                <code class="dest">/{{ source.dest }}/</code>
              </td>
              <td class="run-timestamp center">
                <StackedTime
                  :value="time(latest(source.id)?.created_at)"
                  :datetime="latest(source.id)?.created_at"
                />
              </td>
              <td class="center"><StatusBadge :state="latest(source.id)?.state ?? 'idle'" /></td>
              <td class="center">
                <div class="row-actions">
                  <button
                    class="icon-button outlined tone-green"
                    :disabled="!source.enabled"
                    :aria-label="'同步 ' + source.name"
                    :title="source.enabled ? '立即同步' : '同步源已禁用'"
                    @click="perform(() => runSource(source.id))"
                  >
                    <Play :size="15" /></button
                  ><button
                    class="icon-button outlined"
                    :aria-label="'编辑 ' + source.name"
                    title="编辑"
                    @click="editSource(source)"
                  >
                    <Pencil :size="15" /></button
                  ><button
                    class="icon-button outlined danger"
                    :aria-label="'删除 ' + source.name"
                    title="删除"
                    @click="perform(() => remove(source))"
                  >
                    <Trash2 :size="15" />
                  </button>
                </div>
              </td>
            </tr>
            <tr v-if="!sources.length">
              <td
                colspan="6"
                class="empty-state"
              >
                <FolderSync :size="32" />
                <h3>这里还没有同步源</h3>
                <p>新增一个源，或调整当前搜索条件。</p>
                <button
                  class="button primary"
                  @click="editSource()"
                >
                  <Plus :size="16" />新增同步源
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <PaginationBar
        v-model:page="page"
        v-model:page-size="pageSize"
        :total="sources.length"
        :count="visible.length"
        noun="个同步源"
      />
    </div>
    <ModalTransition>
      <SourceEditor
        v-if="showEditor"
        :key="selected"
        :source="edit"
        @close="selected = ''"
      />
    </ModalTransition>
  </section>
</template>

<style scoped>
.source-table {
  min-width: 980px;
  table-layout: fixed;
}
.source-table :deep(th:first-child) {
  width: 250px;
}
.source-table :deep(th:nth-child(2)) {
  width: 220px;
}
.source-table :deep(th:nth-child(3)) {
  width: 170px;
}
.source-table :deep(th:nth-child(4)) {
  width: 130px;
}
.source-table :deep(th:nth-child(5)) {
  width: 110px;
}
.source-table :deep(th:last-child) {
  width: 160px;
}
.source-table :deep(td:first-child) {
  min-width: 0;
}
.source-table :deep(.run-timestamp) {
  white-space: nowrap;
}
.source-name {
  display: flex;
  gap: var(--space-12px);
  align-items: center;
  min-width: 0;
}
.source-name > :deep(div) {
  min-width: 0;
}
.source-name :deep(strong) {
  font-size: var(--font-15px);
  font-weight: 600;
  white-space: normal;
  overflow-wrap: anywhere;
}
.source-name :deep(small) {
  font-size: var(--font-12d5px);
  display: flex;
  gap: var(--space-8px);
  align-items: center;
  font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
}
.enabled-dot {
  display: inline-flex;
  align-items: center;
  gap: var(--space-5px);
  color: var(--ok);
  font-family:
    -apple-system, BlinkMacSystemFont, 'PingFang SC', 'Segoe UI', 'Microsoft YaHei', sans-serif;
}
.enabled-dot::before {
  content: '';
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--ok);
}
.enabled-dot.disabled {
  color: var(--t3);
}
.enabled-dot.disabled::before {
  background: transparent;
  box-shadow: inset 0 0 0 1px var(--t3);
}
.source-table :deep(.type-pill) {
  margin-top: var(--space-8px);
}
.source-location {
  position: relative;
}
.source-origin {
  display: block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--t2);
  font-size: var(--font-12d5px);
}
.backup-count {
  font-size: var(--font-12d5px);
  color: var(--t3);
  display: block;
  margin-top: var(--space-3px);
}
.source-location :deep(.backup-count) {
  position: absolute;
  top: 100%;
  left: 0;
  white-space: nowrap;
}
</style>
