<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { RefreshCw, ShieldCheck } from '@lucide/vue'
import { api } from '../../app/api'
import { perform } from '../../app/state'
import type { SystemChecks } from '../../app/types'

const checks = ref<SystemChecks>()
const checking = ref(false)
const controller = new AbortController()
function diskSize(bytes: number | undefined) {
  if (bytes === undefined) return '未知'
  return bytes >= 1024 ** 4
    ? `${(bytes / 1024 ** 4).toFixed(2)} TiB`
    : `${(bytes / 1024 ** 3).toFixed(2)} GiB`
}
function toolVersion(tool: SystemChecks['rsync'] | undefined, name: 'rsync' | 'dnf' | 'rpmkeys') {
  if (!tool?.available) {
    if (name === 'rpmkeys') return 'Reposync 开启 GPG 校验时必需；未检测到 RPM 公钥工具'
    return name === 'dnf' ? '可选；未检测到可用的 DNF' : '未检测到可用的 Rsync'
  }
  const patterns = {
    rsync: /\brsync\s+version\s+(\d+(?:\.\d+)+)/i,
    rpmkeys: /RPM version\s+(\d+(?:\.\d+)+)/i,
    dnf: /^\s*(\d+(?:\.\d+)+)\s*$/m,
  }
  const version = tool.version.match(patterns[name])?.[1]
  return version ? `版本 ${version}` : '可执行，版本未识别'
}
const checkRows = computed(() => {
  const result = checks.value
  return [
    {
      label: 'Rsync',
      status: result?.rsync.available ? '可用' : '不可用',
      ok: result?.rsync.available,
      detail: toolVersion(result?.rsync, 'rsync'),
    },
    {
      label: 'DNF',
      status: result?.dnf.available ? '可用' : '不可用',
      ok: result?.dnf.available,
      detail: `${toolVersion(result?.dnf, 'dnf')}；未检查 reposync 插件`,
    },
    {
      label: 'RPM 公钥工具',
      status: result?.rpmkeys?.available ? '可用' : '不可用',
      ok: result?.rpmkeys?.available,
      detail: toolVersion(result?.rpmkeys, 'rpmkeys'),
    },
    {
      label: '配置目录',
      status: result?.config_writable ? '可写' : '不可写',
      ok: result?.config_writable,
      detail: 'YAML 原子写回需要在此目录创建临时文件',
    },
    {
      label: '镜像根目录',
      status: result?.mirror_writable ? '可写' : '不可写',
      ok: result?.mirror_writable,
      detail: '同步与发布使用此目录',
    },
    {
      label: 'Reflink',
      status:
        result?.reflink?.status === 'supported'
          ? '支持'
          : result?.reflink?.status === 'unsupported'
            ? '不支持'
            : '探测失败',
      ok: result?.reflink?.status === 'supported',
      detail: result?.reflink?.detail ?? '尚未取得镜像目录的实测结果',
    },
    {
      label: '镜像盘空间',
      status:
        !result?.storage || result.storage.error
          ? '读取失败'
          : result.storage.available < result.storage.required
            ? '余量不足'
            : '正常',
      ok:
        !!result?.storage &&
        !result.storage.error &&
        result.storage.available >= result.storage.required,
      detail:
        result?.storage?.error ??
        `可用 ${diskSize(result?.storage?.available)} / 总量 ${diskSize(result?.storage?.total)}；安全余量 ${diskSize(result?.storage?.reserve)}，任务启动需余量 ${diskSize(result?.storage?.required)}；旧代 ${result?.storage?.previous ?? 0}，任务目录 ${result?.storage?.work_directories ?? 0}。共享数据块不按目录重复计量。`,
    },
    {
      label: 'GitHub Token',
      status: result?.github_token_configured ? '已配置' : '未配置',
      ok: result?.github_token_configured,
      detail: '仅检查配置状态，未验证 API 认证',
    },
    {
      label: '下载代理',
      status: result?.github_proxy_configured ? '已配置' : '直连',
      ok: true,
      detail: '仅检查配置状态，未测试网络连通性',
    },
  ]
})
async function check() {
  if (checking.value) return
  checking.value = true
  checks.value = undefined
  try {
    checks.value = await api<SystemChecks>('/system/checks', 'GET', undefined, controller.signal)
  } finally {
    checking.value = false
  }
}
onMounted(() => {
  void perform(check)
})
onBeforeUnmount(() => controller.abort())
</script>

<template>
  <article
    class="panel system-panel environment-checks"
    :aria-busy="checking"
  >
    <div class="system-panel-heading">
      <ShieldCheck :size="19" />
      <h2>环境检查</h2>
      <small>{{ checking ? '正在检查…' : checks ? '检查完成' : '尚未取得检查结果' }}</small
      ><button
        class="button compact"
        :disabled="checking"
        @click="perform(check)"
      >
        <RefreshCw :size="16" />{{ checking ? '刷新中…' : '刷新' }}
      </button>
    </div>
    <div
      v-if="checks"
      class="checks-list"
    >
      <div
        v-for="row in checkRows"
        :key="row.label"
        class="check-row"
      >
        <strong>{{ row.label }}</strong
        ><span
          class="check-status"
          :class="row.ok ? 'ready' : 'unavailable'"
          >{{ row.status }}</span
        >
        <p>{{ row.detail }}</p>
      </div>
    </div>
    <p
      v-else
      class="check-empty"
      role="status"
    >
      {{
        checking ? '正在读取工具版本、目录权限和镜像存储能力…' : '检查未完成，请点击“刷新”重试。'
      }}
    </p>
  </article>
</template>

<style scoped>
.system-panel {
  padding: 24px 0 0;
  margin-top: 8px;
  border: 0;
  border-top: 1px solid var(--line-strong);
  border-radius: 0;
  background: transparent;
}
.system-panel h2 {
  font-size: 15px;
}
.system-panel-heading {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.system-panel-heading > svg {
  color: var(--t3);
  flex-shrink: 0;
}
.system-panel-heading small {
  margin-left: auto;
}
.check-empty {
  color: var(--t3);
  font-size: 13px;
  line-height: 1.8;
}
.check-row {
  display: grid;
  grid-template-columns: 130px 80px minmax(0, 1fr);
  align-items: center;
  gap: 24px;
  border-top: 1px solid var(--line);
  padding: 15px 0;
  font-size: 13px;
}
.check-row strong {
  font-weight: 400;
}
.check-row p {
  color: var(--t2);
  line-height: 1.7;
}
.check-status {
  justify-self: center;
  display: inline-flex;
  align-items: center;
  gap: 7px;
  width: fit-content;
  font-size: 13px;
  white-space: nowrap;
}
.check-status::before {
  content: '';
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
.ready {
  color: var(--t2);
}
.ready::before {
  background: var(--acc);
}
.unavailable {
  color: var(--amb);
}
@media (max-width: 600px) {
  .check-row {
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 10px;
  }
  .check-row p {
    grid-column: 1 / -1;
  }
}
</style>
