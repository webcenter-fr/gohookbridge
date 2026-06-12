<template>
  <n-code :code="formatted" language="json" />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NCode } from 'naive-ui'

const props = defineProps<{
  data: any
}>()

const formatted = computed(() => {
  try {
    return JSON.stringify(props.data, null, 2)
  } catch {
    return String(props.data)
  }
})
</script>