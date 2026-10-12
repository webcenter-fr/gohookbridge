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
        <UFormField label="Trusted Proxy CIDRs">
          <div class="flex flex-col gap-2 w-full">
            <div v-for="(cidr, i) in form.server.trusted_proxy_cidrs" :key="i" class="flex items-center gap-2">
              <UInput v-model="form.server.trusted_proxy_cidrs[i]" placeholder="10.0.0.0/8" class="flex-1" />
              <UButton icon="i-lucide-trash" color="error" variant="ghost" @click="form.server.trusted_proxy_cidrs.splice(i, 1)" />
            </div>
            <UButton icon="i-lucide-plus" color="neutral" variant="soft" class="self-start" @click="form.server.trusted_proxy_cidrs.push('')">Add CIDR</UButton>
          </div>
          <template #hint>
            Forwarded headers (X-Forwarded-For/X-Real-IP) are honored only from these networks.
          </template>
        </UFormField>
        <UFormField label="Require Known Channels">
          <USwitch v-model="form.server.require_known_channels" />
          <template #hint>Reject webhooks for channels that are not configured.</template>
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

        <USeparator />
        <h4 class="text-base font-semibold m-0">Login Throttling</h4>
        <UFormField label="Attempts per Account">
          <UInputNumber v-model="form.server.login_throttle_attempts" :min="1" />
        </UFormField>
        <UFormField label="Cooldown (seconds)">
          <UInputNumber v-model="form.server.login_throttle_cooldown_seconds" :min="1" />
        </UFormField>

        <USeparator />
        <h4 class="text-base font-semibold m-0">Default Channel Config</h4>
        <UFormField label="Default Webhook Secret">
          <UBadge :color="form.defaults.webhook_secret_configured ? 'success' : 'neutral'" size="sm">
            {{ form.defaults.webhook_secret_configured ? 'Configured (hidden)' : 'Not configured' }}
          </UBadge>
        </UFormField>
        <UFormField label="Default Allowed IPs">
          <div class="flex flex-col gap-2 w-full">
            <div v-for="(ip, i) in form.defaults.allowed_ips" :key="i" class="flex items-center gap-2">
              <UInput v-model="form.defaults.allowed_ips[i]" placeholder="10.0.0.0/8" class="flex-1" />
              <UButton icon="i-lucide-trash" color="error" variant="ghost" @click="form.defaults.allowed_ips.splice(i, 1)" />
            </div>
            <UButton icon="i-lucide-plus" color="neutral" variant="soft" class="self-start" @click="form.defaults.allowed_ips.push('')">Add IP</UButton>
          </div>
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
    trusted_proxy_cidrs: [] as string[],
    require_known_channels: false,
    rate_limit_enabled: false,
    rate_limit_requests: 100,
    rate_limit_window_seconds: 60,
    ban_enabled: false,
    ban_max_unique_failures: 5,
    ban_window_seconds: 300,
    ban_duration_seconds: 3600,
    login_throttle_attempts: 10,
    login_throttle_cooldown_seconds: 300,
  },
  defaults: {
    webhook_secret_configured: false,
    allowed_ips: [] as string[],
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
    form.server.trusted_proxy_cidrs = (config.value.server.trusted_proxy_cidrs?.length ? config.value.server.trusted_proxy_cidrs : [])
    form.server.require_known_channels = config.value.server.require_known_channels
    form.defaults.webhook_secret_configured = !!config.value.defaults.webhook_secret_configured
    form.defaults.allowed_ips = (config.value.defaults.allowed_ips?.length ? config.value.defaults.allowed_ips : [])
    form.defaults.message_ttl_seconds = config.value.defaults.message_ttl_seconds || 0
    form.server.rate_limit_enabled = config.value.server.rate_limit_enabled
    form.server.rate_limit_requests = config.value.server.rate_limit_requests || 100
    form.server.rate_limit_window_seconds = config.value.server.rate_limit_window_seconds || 60
    form.server.ban_enabled = config.value.server.ban_enabled
    form.server.ban_max_unique_failures = config.value.server.ban_max_unique_failures || 5
    form.server.ban_window_seconds = config.value.server.ban_window_seconds || 300
    form.server.ban_duration_seconds = config.value.server.ban_duration_seconds || 3600
    form.server.login_throttle_attempts = config.value.server.login_throttle_attempts || 10
    form.server.login_throttle_cooldown_seconds = config.value.server.login_throttle_cooldown_seconds || 300
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
    const clean = (arr: string[]) => arr.filter(s => s.trim() !== '')
    await api.updateGlobalConfig({
      server: {
        ...form.server,
        max_body_size: maxBodyBytes,
        trusted_proxy_cidrs: clean(form.server.trusted_proxy_cidrs),
      },
      defaults: {
        allowed_ips: clean(form.defaults.allowed_ips),
        message_ttl_seconds: form.defaults.message_ttl_seconds,
      },
    })
    toast.add({ title: 'Saved', color: 'success' })
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to save', color: 'error' })
  } finally {
    saving.value = false
  }
}
</script>
