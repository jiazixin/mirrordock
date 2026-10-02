<script setup lang="ts">
defineProps<{ state: string }>()
const labels: Record<string, string> = {
  idle: '尚未运行',
  queued: '排队中',
  running: '运行中',
  pausing: '正在暂停',
  paused: '已暂停',
  succeeded: '成功',
  failed: '失败',
  timed_out: '超时',
  cancelled: '已取消',
  interrupted: '运行中断',
}
</script>
<template>
  <span
    class="badge"
    :class="state"
    ><span
      class="badge-dot"
      aria-hidden="true"
    ></span
    >{{ labels[state] ?? state }}</span
  >
</template>
