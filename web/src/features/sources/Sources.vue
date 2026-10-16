<script setup lang="ts">
import Pagination from '../../components/Pagination.vue'
import { usePagination } from '../../lib/pagination/usePagination'
import SelectField from '../../components/SelectField.vue'
import { computed, ref } from 'vue'
import { Plus, RefreshCw, Search, Play, Pencil, Trash2, FolderSync } from '@lucide/vue'
import SourceEditor from './SourceEditor.vue'
import StatusBadge from '../../components/StatusBadge.vue'
import { clone, state, perform, refresh, runSource, saveConfiguration } from '../../app/state'
import type { Source } from '../../app/types'
import StackedTime from '../../components/StackedTime.vue'
import { time, bytes } from '../../app/format'
const search = ref('')
const type = ref('')
const edit = ref<Source | undefined>()
const showEditor = ref(false)
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
const active = computed(
  () => state.runs.filter((run) => ['running', 'queued', 'pausing'].includes(run.state)).length,
)
function latest(id: string) {
  return state.runs.find((run) => run.source_id === id)
}
function editSource(source?: Source) {
  edit.value = source
  showEditor.value = true
}
async function remove(source: Source) {
  if (!window.confirm(`删除同步源“${source.name}”？已保存的文件与运行记录会保留。`)) return
  if (!state.config) return
  const config = clone(state.config.config)
  config.sources = config.sources.filter((item) => item.id !== source.id)
  await saveConfiguration(config)
}
</script>
<template>
  <section>
    <div class="page-heading">
      <div>
        <h1>同步源</h1>
      </div>
      <div class="heading-actions">
        <button
          class="button"
          @click="perform(refresh)"
        >
          <RefreshCw :size="16" />刷新</button
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
        ><strong class="bytes-stat">{{ bytes(state.info?.downloads.bytes ?? 0) }}</strong
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
            :options="[
              { value: '', label: '同步方式' },
              { value: 'rsync', label: 'rsync' },
              { value: 'reposync', label: 'reposync' },
              { value: 'github_release', label: 'GitHub Release' },
            ]"
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
              <th>最近运行</th>
              <th>状态</th>
              <th>操作</th>
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
                  >{{ source.type === 'github_release' ? 'GitHub Release' : source.type }}</span
                >
              </td>
              <td>
                <div class="source-location">
                  <code
                    class="source-origin"
                    :title="
                      source.type === 'github_release'
                        ? source.github_release.repository
                        : source.upstreams.src1
                    "
                    >{{
                      source.type === 'github_release'
                        ? source.github_release.repository
                        : source.upstreams.src1
                    }}</code
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
              <td class="run-timestamp">
                <StackedTime
                  :value="time(latest(source.id)?.created_at)"
                  :datetime="latest(source.id)?.created_at"
                />
              </td>
              <td><StatusBadge :state="latest(source.id)?.state ?? 'idle'" /></td>
              <td class="right">
                <div class="row-actions">
                  <button
                    class="button compact"
                    :disabled="!source.enabled"
                    @click="perform(() => runSource(source.id))"
                  >
                    <Play :size="14" />同步</button
                  ><button
                    class="icon-button"
                    :aria-label="'编辑 ' + source.name"
                    @click="editSource(source)"
                  >
                    <Pencil :size="16" /></button
                  ><button
                    class="icon-button danger"
                    :aria-label="'删除 ' + source.name"
                    @click="perform(() => remove(source))"
                  >
                    <Trash2 :size="16" />
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
      <Pagination
        v-model:page="page"
        v-model:page-size="pageSize"
        :total="sources.length"
        :count="visible.length"
        noun="个同步源"
      />
    </div>
    <SourceEditor
      v-if="showEditor"
      :source="edit"
      @close="showEditor = false"
    />
  </section>
</template>
