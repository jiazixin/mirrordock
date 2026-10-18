<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { X } from '@lucide/vue'
import { state } from '../../app/state'
import {
  createRow,
  joinDest,
  parseRepositories,
  staggerCrons,
  validateRows,
  type BatchRow,
} from './githubBatch'
const props = defineProps<{ cron: string; sharedPatterns: string[] }>()
const rows = defineModel<BatchRow[]>('rows', { required: true })
// The textarea starts empty on every mount, so stale rows must not survive.
rows.value = []
const text = ref('')
const prefix = ref('github')
const parsed = computed(() => parseRepositories(text.value))
const existing = computed(() => state.config?.config.sources ?? [])
const errors = computed(() => validateRows(rows.value, existing.value))
const schedule = computed(() => staggerCrons(props.cron, rows.value.length))
const configured = computed(
  () =>
    new Set(
      existing.value
        .filter((source) => source.type === 'github_release')
        .map((source) => source.github_release.repository.toLowerCase()),
    ),
)

// Keep edited rows for repositories that stay in the list and generate
// rows only for newly entered ones, in input order.
watch(
  () => parsed.value.repositories,
  (repositories) => {
    const current = new Map(rows.value.map((row) => [row.repository.toLowerCase(), row]))
    const kept = repositories.flatMap((repository) => current.get(repository.toLowerCase()) ?? [])
    const takenIds = new Set([...existing.value.map((s) => s.id), ...kept.map((row) => row.id)])
    const takenDests = [...existing.value.map((s) => s.dest), ...kept.map((row) => row.dest)]
    rows.value = repositories.map((repository) => {
      const row = current.get(repository.toLowerCase())
      if (row) return row
      const created = createRow(repository, prefix.value, takenIds, takenDests)
      takenIds.add(created.id)
      takenDests.push(created.dest)
      return created
    })
  },
)

// A prefix change only moves dest values the user has not edited.
watch(prefix, (value) => {
  for (const row of rows.value) {
    if (row.dest !== row.generatedDest) continue
    row.dest = joinDest(value, row.slug)
    row.generatedDest = row.dest
  }
})

function remove(index: number) {
  const repository = rows.value[index]!.repository.toLowerCase()
  // Unrecognized entries stay in the textarea so the user can still fix them.
  text.value = [
    ...parsed.value.repositories.filter((item) => item.toLowerCase() !== repository),
    ...parsed.value.invalid,
  ].join('\n')
}
</script>

<template>
  <div class="batch">
    <label
      >GitHub 仓库<textarea
        v-model="text"
        rows="5"
        placeholder="一行一个，例如 OpenListTeam/OpenList 或 https://github.com/owner/repo"
      /><small>也可用空格或逗号分隔；重复的仓库只保留一个。</small></label
    >
    <div
      v-if="parsed.invalid.length"
      class="alert warning"
      role="status"
    >
      无法识别，已忽略：{{ parsed.invalid.join('、') }}
    </div>
    <label class="batch-prefix"
      >目的目录前缀<input
        v-model="prefix"
        placeholder="github"
      /><small
        >dest 默认为「前缀/仓库名」，留空则直接用仓库名；手动改过的 dest 不随前缀变化。</small
      ></label
    >
    <div
      v-if="rows.length"
      class="batch-preview"
    >
      <h4>
        预览 {{ rows.length }} 个同步源<span>{{
          schedule
            ? `Cron 分钟按 ${schedule.step} 分钟间隔错开`
            : 'Cron 分钟字段不是单个数字，所有源使用同一 Cron'
        }}</span>
      </h4>
      <div class="table-scroll">
        <table>
          <colgroup>
            <col class="col-repo" />
            <col span="4" />
            <col class="col-action" />
          </colgroup>
          <thead>
            <tr>
              <th>仓库</th>
              <th class="center">唯一标识</th>
              <th class="center">源名称</th>
              <th class="center">目的目录 · dest</th>
              <th class="center">附件 glob</th>
              <th><span class="sr-only">操作</span></th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="(row, index) in rows"
              :key="row.repository"
            >
              <td>
                <code class="repo">{{ row.repository }}</code
                ><small
                  >Cron <code class="cron">{{ schedule?.crons[index] ?? cron }}</code></small
                ><small
                  v-if="configured.has(row.repository.toLowerCase())"
                  class="existing"
                  >已有同仓库的源</small
                >
              </td>
              <td>
                <input
                  v-model.trim="row.id"
                  :aria-label="row.repository + ' 唯一标识'"
                  :aria-invalid="!!errors[index]?.id"
                /><small
                  v-if="errors[index]?.id"
                  class="error-text"
                  >{{ errors[index]?.id }}</small
                >
              </td>
              <td>
                <input
                  v-model="row.name"
                  :aria-label="row.repository + ' 源名称'"
                  :aria-invalid="!!errors[index]?.name"
                /><small
                  v-if="errors[index]?.name"
                  class="error-text"
                  >{{ errors[index]?.name }}</small
                >
              </td>
              <td>
                <input
                  v-model.trim="row.dest"
                  class="mono"
                  :aria-label="row.repository + ' 目的目录'"
                  :aria-invalid="!!errors[index]?.dest"
                /><small
                  v-if="errors[index]?.dest"
                  class="error-text"
                  >{{ errors[index]?.dest }}</small
                >
              </td>
              <td>
                <input
                  v-model="row.patterns"
                  class="mono"
                  :aria-label="row.repository + ' 附件 glob'"
                  :placeholder="sharedPatterns.length ? '沿用共用规则' : '全部附件'"
                  :title="sharedPatterns.join(', ')"
                />
              </td>
              <td class="center">
                <button
                  type="button"
                  class="icon-button outlined danger"
                  :aria-label="'移除 ' + row.repository"
                  title="移除"
                  @click="remove(index)"
                >
                  <X :size="15" />
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <small
        >附件 glob 用逗号分隔，留空沿用下方共用规则。保存时一次写回
        YAML，任一行无效则全部不保存。</small
      >
    </div>
  </div>
</template>

<style scoped>
.batch {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.batch-prefix {
  max-width: 50%;
}
.batch-preview {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.batch-preview h4 {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  font-size: 14px;
  font-weight: 500;
}
.batch-preview h4 span {
  font-size: 12px;
  color: var(--t3);
  font-weight: 400;
}
.table-scroll {
  border: 1px solid var(--line);
  border-radius: 10px;
}
table {
  table-layout: fixed;
  min-width: 680px;
  font-size: 13px;
}
.col-repo {
  width: 24%;
}
.col-action {
  width: 52px;
}
th {
  padding: 10px 8px;
  font-size: 12.5px;
}
th:first-child,
td:first-child {
  padding-left: 14px;
}
td {
  padding: 10px 8px;
  vertical-align: top;
}
tbody tr:hover td {
  background: transparent;
}
td input {
  padding: 6px 9px;
  font-size: 13px;
}
td input[aria-invalid='true'] {
  border-color: var(--red);
}
.mono,
.repo,
.cron {
  font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12px;
}
.repo {
  display: block;
  padding-top: 7px;
  overflow-wrap: anywhere;
}
.cron {
  white-space: nowrap;
}
.existing {
  color: var(--amb);
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}
@media (max-width: 600px) {
  .batch-prefix {
    max-width: none;
  }
}
</style>
