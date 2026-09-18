<template>
  <pre class="bg-(--ui-bg-elevated) rounded p-3 overflow-x-auto text-sm font-mono"><code>{{ code }}</code></pre>
</template>

<script setup lang="ts">
defineProps<{
  code: string
  language?: string
}>()
</script>
