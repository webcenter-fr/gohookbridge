<template>
  <div class="json-viewer">
    <div class="toggle-bar" @click="toggleExpanded">
      <span class="toggle-icon">{{ expanded ? '▼' : '▶' }}</span>
    </div>
    <n-code :code="displayText" language="json" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { NCode } from 'naive-ui'

const props = defineProps<{
  data: any
  expanded?: boolean
}>()

const expanded = ref(props.expanded ?? false)

function toggleExpanded() {
  expanded.value = !expanded.value
}

function decodeBodyB(obj: any): any {
  if (!obj || typeof obj !== 'object') return obj
  const result: Record<string, any> = {}
  for (const [key, value] of Object.entries(obj)) {
    if (key === 'bodyB' && typeof value === 'string') {
      try {
        const binary = atob(value)
        const bytes = new Uint8Array(binary.length)
        for (let i = 0; i < binary.length; i++) {
          bytes[i] = binary.charCodeAt(i)
        }
        const decoded = new TextDecoder().decode(bytes)
        try {
          result[key] = JSON.parse(decoded)
        } catch {
          result[key] = decoded
        }
      } catch {
        result[key] = value
      }
    } else {
      result[key] = value
    }
  }
  return result
}

const displayText = computed(() => {
  try {
    const displayData = decodeBodyB(props.data)
    if (expanded.value) {
      return JSON.stringify(displayData, null, 2)
    }
    return JSON.stringify(displayData)
  } catch {
    return String(props.data)
  }
})
</script>

<style scoped>
.json-viewer {
  width: 100%;
}
.toggle-bar {
  cursor: pointer;
  user-select: none;
  padding: 2px 0;
}
.toggle-icon {
  font-size: 12px;
  color: #888;
}
</style>