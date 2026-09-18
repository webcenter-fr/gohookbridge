<template>
  <div class="flex flex-col gap-4">
    <h3 class="text-lg font-semibold m-0">Global Configuration</h3>
    <div :class="{ 'opacity-50 pointer-events-none': loading }">
      <form v-if="config" class="flex flex-col gap-4 max-w-[600px]" @submit.prevent="handleSave">
        <UFormField label="Max Body Size">
          <div class="flex items-center gap-2">
            <UInputNumber v-model="form.server.max_body_size" :min="0" class="w-[180px]" />
            <USelect v-model="bodySizeUnit" :items="bodySizeUnits" class="w-[100px]" />
          </div>
        </UFormField>
        <UFormField label="Default Message TTL (seconds)">
          <UInputNumber v-model="form.defaults.message_ttl_seconds" :min="0" placeholder="0 = use NATS buffer TTL" />
        </UFormField>
        <UFormField label="Behind Reverse Proxy">
          <USwitch v-model="form.server.behind_reverse_proxy" />
        </UFormField>
        <UFormField label="CORS Origin">
          <UInput v-model="form.server.cors_origin" placeholder="*" class="w-full" />
        </UFormField>
        <UFormField label="Footer (HTML)">
          <UTextarea v-model="form.server.footer" :rows="3" class="w-full" />
        </UFormField>

        <USeparator />
        <h4 class="text-base font-semibold m-0">Rate Limiting</h4>
        <UFormField label="Enable Rate Limiting">
          <USwitch v-model="form.server.rate_limit_enabled" />
        </UFormField>
        <UFormField label="Requests per Window">
          <UInputNumber v-model="form.server.rate_limit_requests" :min="1" />
        </UFormField>
        <UFormField label="Window Duration (seconds)">
          <UInputNumber v-model="form.server.rate_limit_window_seconds" :min="1" />
        </UFormField>

        <USeparator />
        <h4 class="text-base font-semibold m-0">IP Ban</h4>
        <UFormField label="Enable IP Ban">
          <USwitch v-model="form.server.ban_enabled" />
        </UFormField>
        <UFormField label="Different Credentials Threshold">
          <UInputNumber v-model="form.server.ban_max_unique_failures" :min="1" />
          <template #hint>
            Number of different credentials (tokens, logins, webhook secrets) that must fail from the same IP within the time window to trigger a ban. Repeated failures with the same credential are ignored (misconfiguration, not attack).
          </template>
        </UFormField>
        <UFormField label="Failure Window (seconds)">
          <UInputNumber v-model="form.server.ban_window_seconds" :min="1" />
        </UFormField>
        <UFormField label="Ban Duration (seconds)">
          <UInputNumber v-model="form.server.ban_duration_seconds" :min="1" />
        </UFormField>

        <UButton type="submit" color="primary" :loading="saving" class="self-start">Save</UButton>
      </form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { api, type GlobalConfig } from '~/utils/api'
import { bodySizeToBytes, bytesToBodySizeUnit, bodySizeUnitOptions, type BodySizeUnit } from '~/utils/units'

definePageMeta({ middleware: 'admin' })

const toast = useToast()
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
    toast.add({ title: e.message || 'Failed to load config', color: 'error' })
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
    toast.add({ title: 'Saved', color: 'success' })
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to save', color: 'error' })
  } finally {
    saving.value = false
  }
}
</script>
