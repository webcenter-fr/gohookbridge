<template>
  <n-space vertical>
    <n-h3>Global Configuration</n-h3>
    <n-spin :show="loading">
      <n-form v-if="config" label-placement="top" style="max-width: 600px;">
        <n-form-item label="Max Body Size">
          <n-space>
            <n-input-number v-model:value="form.server.max_body_size" :min="0" style="width: 180px;" />
            <n-select v-model:value="bodySizeUnit" :options="bodySizeUnits" style="width: 100px;" />
          </n-space>
        </n-form-item>
        <n-form-item label="Default Message TTL (seconds)">
          <n-input-number v-model:value="form.defaults.message_ttl_seconds" :min="0" placeholder="0 = use NATS buffer TTL" />
        </n-form-item>
        <n-form-item label="Behind Reverse Proxy">
          <n-switch v-model:value="form.server.behind_reverse_proxy" />
        </n-form-item>
        <n-form-item label="CORS Origin">
          <n-input v-model:value="form.server.cors_origin" placeholder="*" />
        </n-form-item>
        <n-form-item label="Footer (HTML)">
          <n-input v-model:value="form.server.footer" type="textarea" :rows="3" />
        </n-form-item>

        <n-divider />
        <n-h4>Rate Limiting</n-h4>
        <n-form-item label="Enable Rate Limiting">
          <n-switch v-model:value="form.server.rate_limit_enabled" />
        </n-form-item>
        <n-form-item label="Requests per Window">
          <n-input-number v-model:value="form.server.rate_limit_requests" :min="1" />
        </n-form-item>
        <n-form-item label="Window Duration (seconds)">
          <n-input-number v-model:value="form.server.rate_limit_window_seconds" :min="1" />
        </n-form-item>

        <n-divider />
        <n-h4>IP Ban</n-h4>
        <n-form-item label="Enable IP Ban">
          <n-switch v-model:value="form.server.ban_enabled" />
        </n-form-item>
        <n-form-item label="Different Credentials Threshold">
          <n-input-number v-model:value="form.server.ban_max_unique_failures" :min="1" />
          <template #feedback>
            Number of different credentials (tokens, logins, webhook secrets) that must fail from the same IP within the time window to trigger a ban. Repeated failures with the same credential are ignored (misconfiguration, not attack).
          </template>
        </n-form-item>
        <n-form-item label="Failure Window (seconds)">
          <n-input-number v-model:value="form.server.ban_window_seconds" :min="1" />
        </n-form-item>
        <n-form-item label="Ban Duration (seconds)">
          <n-input-number v-model:value="form.server.ban_duration_seconds" :min="1" />
        </n-form-item>

        <n-button type="primary" @click="handleSave" :loading="saving">Save</n-button>
      </n-form>
    </n-spin>
  </n-space>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { NSpace, NH3, NH4, NSpin, NForm, NFormItem, NInput, NInputNumber, NSwitch, NButton, NSelect, NDivider } from 'naive-ui'
import { api, type GlobalConfig } from '../api/client'
import { useMessage } from 'naive-ui'
import { bodySizeToBytes, bytesToBodySizeUnit, bodySizeUnitOptions, type BodySizeUnit } from '../utils/units'

const message = useMessage()
const loading = ref(true)
const saving = ref(false)
const config = ref<GlobalConfig | null>(null)

const bodySizeUnit = ref<BodySizeUnit>('bytes')
const bodySizeUnits = bodySizeUnitOptions

const form = reactive({
  server: {
    max_body_size: 26214400,
    behind_reverse_proxy: false,
    cors_origin: '*',
    footer: '',
    rate_limit_enabled: false,
    rate_limit_requests: 100,
    rate_limit_window_seconds: 60,
    ban_enabled: false,
    ban_max_unique_failures: 5,
    ban_window_seconds: 300,
    ban_duration_seconds: 3600,
  },
  defaults: {
    message_ttl_seconds: 0,
  },
})

onMounted(async () => {
  try {
    config.value = await api.getGlobalConfig()
    const bs = bytesToBodySizeUnit(config.value.server.max_body_size)
    form.server.max_body_size = Math.round(bs.value)
    bodySizeUnit.value = bs.unit
    form.server.behind_reverse_proxy = config.value.server.behind_reverse_proxy
    form.server.cors_origin = config.value.server.cors_origin
    form.server.footer = config.value.server.footer
    form.defaults.message_ttl_seconds = config.value.defaults.message_ttl_seconds || 0
    form.server.rate_limit_enabled = config.value.server.rate_limit_enabled
    form.server.rate_limit_requests = config.value.server.rate_limit_requests || 100
    form.server.rate_limit_window_seconds = config.value.server.rate_limit_window_seconds || 60
    form.server.ban_enabled = config.value.server.ban_enabled
    form.server.ban_max_unique_failures = config.value.server.ban_max_unique_failures || 5
    form.server.ban_window_seconds = config.value.server.ban_window_seconds || 300
    form.server.ban_duration_seconds = config.value.server.ban_duration_seconds || 3600
  } catch (e: any) {
    message.error(e.message || 'Failed to load config')
  } finally {
    loading.value = false
  }
})

async function handleSave() {
  const maxBodyBytes = bodySizeToBytes(form.server.max_body_size, bodySizeUnit.value)
  saving.value = true
  try {
    await api.updateGlobalConfig({
      server: { ...form.server, max_body_size: maxBodyBytes, session_secret: '' },
      defaults: { webhook_secret: '', allowed_ips: [], message_ttl_seconds: form.defaults.message_ttl_seconds },
    })
    message.success('Saved')
  } catch (e: any) {
    message.error(e.message || 'Failed to save')
  } finally {
    saving.value = false
  }
}
</script>