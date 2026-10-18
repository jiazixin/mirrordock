<script setup lang="ts">
import SelectField from '../../components/SelectField.vue'
import { computed, ref } from 'vue'
import { Plus, Trash2, Save } from '@lucide/vue'
import Modal from '../../components/Modal.vue'
import OptionCard from '../../components/OptionCard.vue'
import type { Source } from '../../app/types'
import { clone, state, saveConfiguration } from '../../app/state'
import GitHubBatch from './GitHubBatch.vue'
import { splitPatterns, staggerCrons, validateRows, type BatchRow } from './githubBatch'
const props = defineProps<{ source?: Source }>()
const emit = defineEmits<{ close: [] }>()
function blank(): Source {
  return {
    id: '',
    name: '',
    type: 'rsync',
    enabled: true,
    dest: '',
    exclude: [],
    upstreams: {},
    rsync: {
      preserve_links: true,
      preserve_times: true,
      preserve_permissions: true,
      preserve_hardlinks: true,
      delete: true,
      uid: null,
      gid: null,
    },
    reposync: {
      repo_id: '',
      releasever: '9',
      basearch: 'x86_64',
      architectures: ['x86_64', 'noarch'],
      download_metadata: true,
      newest_only: false,
      delete: true,
      gpgcheck: true,
      gpg_keys: [],
      module_hotfixes: false,
    },
    github_release: { repository: '', release_count: 0, asset_patterns: [] },
    schedule: { enabled: false, cron: '0 2 * * *', overlap: 'skip' },
  }
}
const draft = ref<Source>(props.source ? clone(props.source) : blank())
const upstreams = ref(
  Object.entries(draft.value.upstreams ?? {})
    .sort(([a], [b]) => Number(a.slice(3)) - Number(b.slice(3)))
    .map(([key, value]) => ({ key, value })),
)
if (!upstreams.value.length) upstreams.value.push({ key: 'src1', value: '' })
const exclusions = ref((draft.value.exclude ?? []).join('\n'))
const patterns = ref((draft.value.github_release.asset_patterns ?? []).join('\n'))
const keys = ref((draft.value.reposync.gpg_keys ?? []).join('\n'))
const architectures = ref((draft.value.reposync.architectures ?? []).join(', '))
const error = ref('')
const busy = ref(false)
const batchMode = ref(false)
const batchRows = ref<BatchRow[]>([])
const batch = computed(
  () => batchMode.value && !props.source && draft.value.type === 'github_release',
)
const revision = state.config?.revision
const title = computed(() => {
  if (props.source) return '编辑同步源'
  return batch.value ? '批量新增同步源' : '新增同步源'
})
const flags = [
  { key: 'preserve_links', label: '保留符号链接', description: '保留链接结构' },
  { key: 'preserve_times', label: '保留修改时间', description: '沿用上游时间' },
  { key: 'preserve_permissions', label: '保留权限', description: '保留文件权限位' },
  { key: 'preserve_hardlinks', label: '保留硬链接', description: '保持硬链接关系' },
  { key: 'delete', label: '删除上游已移除文件', description: '同步清理上游已删除的文件' },
] as const
const repoFlags = [
  {
    key: 'download_metadata',
    label: '下载 metadata',
    description: '关闭时仅发布 RPM 文件，不提供可直接安装的仓库元数据',
  },
  { key: 'gpgcheck', label: '检查包签名', description: '验证 RPM 包的 GPG 签名' },
  { key: 'delete', label: '清理上游已删除 RPM', description: '同步清理上游已删除的 RPM' },
  { key: 'module_hotfixes', label: 'module_hotfixes', description: '允许仓库包绕过模块过滤' },
] as const
for (const flag of flags) draft.value.rsync[flag.key] ??= true
for (const flag of ['download_metadata', 'gpgcheck', 'delete'] as const)
  draft.value.reposync[flag] ??= true
function lines(value: string) {
  return value
    .split('\n')
    .map((v) => v.trim())
    .filter(Boolean)
}
function addUpstream() {
  const next = Math.max(...upstreams.value.map((v) => Number(v.key.slice(3))), 0) + 1
  upstreams.value.push({ key: `src${next}`, value: '' })
}
function number(key: 'uid' | 'gid', event: Event) {
  const raw = (event.target as HTMLInputElement).value
  draft.value.rsync[key] = raw === '' ? null : Number(raw)
}
// batchSources expands the shared draft into one source per preview row;
// the whole batch is written in a single revision-checked save.
function batchSources(shared: Source, existing: Source[]): Source[] {
  const rows = batchRows.value
  if (!rows.length) throw new Error('请至少填写一个 GitHub 仓库')
  if (validateRows(rows, existing).some((errors) => Object.keys(errors).length))
    throw new Error('请先修正预览表中标红的项')
  const crons = staggerCrons(shared.schedule.cron, rows.length)?.crons
  return rows.map((row, index) => {
    const source = clone(shared)
    source.id = row.id
    source.name = row.name.trim()
    source.dest = row.dest
    const patterns = splitPatterns(row.patterns)
    source.github_release.repository = row.repository
    if (patterns.length) source.github_release.asset_patterns = patterns
    source.schedule.cron = crons?.[index] ?? shared.schedule.cron
    return source
  })
}
async function save() {
  if (!state.config) return
  busy.value = true
  error.value = ''
  try {
    const source = clone(draft.value)
    source.exclude = source.type === 'rsync' ? lines(exclusions.value) : []
    source.upstreams =
      source.type === 'github_release'
        ? {}
        : source.type === 'reposync'
          ? { src1: upstreams.value[0]?.value.trim() ?? '' }
          : Object.fromEntries(upstreams.value.map((item) => [item.key, item.value.trim()]))
    source.github_release.asset_patterns = lines(patterns.value)
    source.reposync.gpg_keys = lines(keys.value)
    source.reposync.architectures = architectures.value
      .split(',')
      .map((v) => v.trim())
      .filter(Boolean)
    const config = clone(state.config.config)
    if (batch.value) config.sources.push(...batchSources(source, config.sources))
    else {
      const index = config.sources.findIndex((item) => item.id === props.source?.id)
      if (index >= 0) config.sources[index] = source
      else config.sources.push(source)
    }
    await saveConfiguration(config, undefined, undefined, revision)
    emit('close')
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <Modal
    :title="title"
    wide
    @close="emit('close')"
    ><form
      class="source-form"
      @submit.prevent="save"
    >
      <div class="form-grid">
        <label v-if="!batch"
          >源名称<input
            v-model="draft.name"
            required
            placeholder="例如 RockyLinux 9" /></label
        ><label v-if="!batch"
          >唯一标识<input
            v-model="draft.id"
            required
            :disabled="!!source"
            pattern="[A-Za-z0-9][A-Za-z0-9._-]*"
            placeholder="rockylinux-9" /></label
        ><label
          >同步方式<SelectField
            v-model="draft.type"
            aria-label="同步方式"
            :options="
              [
                { value: 'rsync', label: 'Rsync' },
                { value: 'reposync', label: 'DNF Reposync' },
                { value: 'github_release', label: 'GitHub Release' },
              ] satisfies { value: Source['type']; label: string }[]
            " /></label
        ><label v-if="!batch"
          >目的目录 · dest<input
            v-model="draft.dest"
            required
            placeholder="rockylinux/9"
          /><small>镜像地址：/{{ draft.dest || 'dest' }}/</small></label
        >
      </div>
      <div
        v-if="!source && draft.type === 'github_release'"
        class="batch-switch"
      >
        <OptionCard
          v-model="batchMode"
          label="批量添加"
          description="一次填入多个仓库，共用下方的 Release 数量、附件规则与运行计划"
        />
      </div>
      <section class="form-section">
        <h3>运行计划</h3>
        <label
          >Cron 表达式<input
            v-model="draft.schedule.cron"
            placeholder="0 2 * * *"
          /><small>五字段，使用程序启动时区；任务重叠默认跳过。</small></label
        >
        <div class="source-option-grid">
          <OptionCard
            v-model="draft.schedule.enabled"
            label="启用自动计划"
            description="按 cron 定时执行"
          /><OptionCard
            v-model="draft.enabled"
            label="启用此同步源"
            description="允许手动及计划同步"
          />
        </div>
      </section>
      <section
        class="form-section"
        v-if="draft.type !== 'github_release'"
      >
        <h3>同步来源<span>按数字顺序尝试，切换不重置任务预算</span></h3>
        <p v-if="draft.type === 'rsync'">地址须包含模块；模块内目录须以 / 结尾。</p>
        <div
          class="upstream-row"
          v-for="(upstream, index) in upstreams"
          :key="upstream.key"
          v-show="draft.type === 'rsync' || index === 0"
        >
          <span>{{ upstream.key }}</span
          ><input
            v-model="upstream.value"
            :required="draft.type === 'rsync' || index === 0"
            :placeholder="
              draft.type === 'rsync'
                ? 'rsync://mirror.example/repo/'
                : 'https://nginx.org/packages/centos/9/x86_64/'
            "
          /><button
            v-if="index > 0"
            type="button"
            class="icon-button"
            aria-label="删除备用来源"
            @click="upstreams.splice(index, 1)"
          >
            <Trash2 :size="16" />
          </button>
        </div>
        <button
          v-if="draft.type === 'rsync'"
          type="button"
          class="button subtle"
          @click="addUpstream"
        >
          <Plus :size="15" />添加备用来源
        </button>
      </section>
      <section
        v-if="draft.type === 'rsync'"
        class="form-section"
      >
        <h3>Rsync 选项</h3>
        <label
          >排除规则 · exclude<textarea
            v-model="exclusions"
            rows="3"
            placeholder="一行一条，例如 isos/**"
          />
          <small>排除规则只停止同步，不会删除此前已同步的文件。</small>
        </label>
        <div class="source-option-grid">
          <OptionCard
            v-for="flag in flags"
            :key="flag.key"
            v-model="draft.rsync[flag.key]"
            :label="flag.label"
            :description="flag.description"
          />
        </div>
        <div class="form-grid">
          <label
            >UID · 可选<input
              type="number"
              min="0"
              :value="draft.rsync.uid ?? ''"
              @input="number('uid', $event)"
              placeholder="留空使用程序身份；0 表示 root" /></label
          ><label
            >GID · 可选<input
              type="number"
              min="0"
              :value="draft.rsync.gid ?? ''"
              @input="number('gid', $event)"
              placeholder="留空不做映射"
          /></label>
        </div>
        <small>指定 UID 通常需要接收端具有 root 权限；程序不会自动提权。</small>
      </section>
      <section
        v-if="draft.type === 'github_release'"
        class="form-section"
      >
        <h3>Release 附件</h3>
        <GitHubBatch
          v-if="batch"
          v-model:rows="batchRows"
          :cron="draft.schedule.cron"
          :shared-patterns="lines(patterns)"
        />
        <div class="form-grid">
          <label v-if="!batch"
            >GitHub 仓库<input
              v-model="draft.github_release.repository"
              required
              placeholder="OpenListTeam/OpenList" /></label
          ><label
            >同步最近 N 个 Release<input
              v-model.number="draft.github_release.release_count"
              type="number"
              min="0"
              max="100"
            /><small>0 表示继承系统设置；按发布时间从新到旧。</small></label
          >
        </div>
        <label
          >{{ batch ? '共用附件 glob' : '附件 glob'
          }}<textarea
            v-model="patterns"
            rows="3"
            placeholder="一行一条，例如 *linux*amd64*"
          /></label
        ><small>只下载 Release assets，自动生成的源码归档不参与同步。</small>
      </section>
      <section
        v-if="draft.type === 'reposync'"
        class="form-section"
      >
        <h3>DNF 仓库选项</h3>
        <div class="form-grid">
          <label
            >仓库 ID<input
              v-model="draft.reposync.repo_id"
              required /></label
          ><label
            >发行版版本<input
              v-model="draft.reposync.releasever"
              required /></label
          ><label
            >basearch<input
              v-model="draft.reposync.basearch"
              required /></label
          ><label
            >架构范围<input
              v-model="architectures"
              placeholder="x86_64, noarch"
          /></label>
        </div>
        <label
          >GPG 公钥地址<textarea
            v-model="keys"
            rows="2"
            placeholder="一行一个 HTTPS 地址"
          />
        </label>
        <div class="source-option-grid">
          <OptionCard
            v-for="flag in repoFlags"
            :key="flag.key"
            v-model="draft.reposync[flag.key]"
            :label="flag.label"
            :description="flag.description"
          />
        </div>
      </section>
      <div
        v-if="error"
        class="alert error"
        role="alert"
      >
        {{ error }}
      </div>
      <footer class="modal-actions">
        <button
          type="button"
          class="button"
          @click="emit('close')"
        >
          取消</button
        ><button
          class="button primary"
          :disabled="busy"
        >
          <Save :size="16" />{{ busy ? '正在保存…' : '保存并写回 YAML' }}
        </button>
      </footer>
    </form></Modal
  >
</template>

<style scoped>
.batch-switch {
  margin-top: 18px;
}
.source-option-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 24px;
}
@media (max-width: 480px) {
  .source-option-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
