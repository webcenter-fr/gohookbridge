<template>
  <n-space vertical>
    <n-h3>Global Configuration</n-h3>
    <n-spin :show="loading">
      <n-form v-if="config" label-placement="top" style="max-width: 600px;">
        <n-form-item label="Max Body Size">
          <n-input-number v-model:value="form.server.max_body_size" :min="0" />
        </n-form-item>
        <n-form-item label="Trust Proxy">
          <n-switch v-model:value="form.server.trust_proxy" />
        </n-form-item>
        <n-form-item label="CORS Origin">
          <n-input v-model:value="form.server.cors_origin" placeholder="*" />
        </n-form-item>
        <n-form-item label="Footer (HTML)">
          <n-input v-model:value="form.server.footer" type="textarea" :rows="3" />
        </n-form-item>
        <n-button type="primary" @click="handleSave" :loading="saving">Save</n-button>
      </n-form>
    </n-spin>
  </n-space>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { NSpace, NH3, NSpin, NForm, NFormItem, NInput, NInputNumber, NSwitch, NButton } from 'naive-ui'
import { api, type GlobalConfig } from '../api/client'
import { useMessage } from 'naive-ui'

const message = useMessage()
const loading = ref(true)
const saving = ref(false)
const config = ref<GlobalConfig | null>(null)

const form = reactive({
  server: {
    max_body_size: 26214400,
    trust_proxy: false,
    cors_origin: '*',
    footer: '',
  },
})

onMounted(async () => {
  try {
    config.value = await api.getGlobalConfig()
    form.server.max_body_size = config.value.server.max_body_size
    form.server.trust_proxy = config.value.server.trust_proxy
    form.server.cors_origin = config.value.server.cors_origin
    form.server.footer = config.value.server.footer
  } catch (e: any) {
    message.error(e.message || 'Failed to load config')
  } finally {
    loading.value = false
  }
})

async function handleSave() {
  saving.value = true
  try {
    await api.updateGlobalConfig({ server: form.server })
    message.success('Saved')
  } catch (e: any) {
    message.error(e.message || 'Failed to save')
  } finally {
    saving.value = false
  }
}
</script>