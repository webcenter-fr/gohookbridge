<template>
  <n-spin :show="loading">
    <n-tabs type="line" :value="tab" @update:value="t => tab = t">
      <n-tab-pane name="data" tab="Data">
        <n-space vertical>
          <event-feed
            :channel="channelId"
            :events="eventsStore.events"
            :connected="eventsStore.connected"
            :connecting="eventsStore.connecting"
            :message-ttl="channel?.message_ttl_seconds"
            :encryption-mode="channel?.encryption_mode"
            @replay="handleReplayEvent"
          />
          <n-space>
            <n-button @click="eventsStore.connect(channelId)" :disabled="eventsStore.connected">Connect</n-button>
            <n-button @click="eventsStore.disconnect()" :disabled="!eventsStore.connected">Disconnect</n-button>
            <n-button @click="showSendDrawer = true" secondary type="info">Send Payload</n-button>
            <n-button @click="eventsStore.clear()" quaternary>Clear</n-button>
          </n-space>
        </n-space>
      </n-tab-pane>

      <n-tab-pane name="settings" tab="Settings">
        <n-form v-if="channel" label-placement="top" style="max-width: 600px;">
          <n-form-item label="Channel ID">
            <n-input :value="channel.id" disabled />
          </n-form-item>
          <n-form-item label="Description">
            <n-input v-model:value="form.description" type="textarea" :maxlength="500" />
          </n-form-item>
          <n-form-item label="Webhook Secret">
            <n-space style="width: 100%;">
              <n-input v-model:value="form.webhook_secret" type="password" show-password-on="click" placeholder="webhook secret" />
              <n-button @click="handleGenerateSecret" secondary>Generate</n-button>
            </n-space>
          </n-form-item>
          <n-form-item label="Allowed IPs">
            <n-dynamic-input v-model:value="form.allowed_ips" placeholder="10.0.0.0/8" />
          </n-form-item>
          <n-form-item label="Max Body Size">
            <n-space>
              <n-input-number v-model:value="form.max_body_size" :min="0" style="width: 180px;" />
              <n-select v-model:value="bodySizeUnit" :options="bodySizeUnits" style="width: 100px;" />
            </n-space>
          </n-form-item>
          <n-form-item label="Message TTL (seconds)">
            <n-input-number v-model:value="form.message_ttl_seconds" :min="0" placeholder="0 = use global default" />
          </n-form-item>
          <n-form-item label="Encryption Mode">
            <n-select v-model:value="form.encryption_mode" :options="encryptionModeOptions" />
          </n-form-item>
          <n-form-item v-if="form.encryption_mode === 'server_side'" label="Encryption Key">
            <n-space style="width: 100%;">
              <n-input v-model:value="form.encryption_key" type="password" show-password-on="click" placeholder="AES-256 key (base64)" />
              <n-button @click="handleGenerateEncryptionKey('server_side')" secondary>Generate Key</n-button>
            </n-space>
            <n-text depth="3" style="margin-top: 4px;">All subscribers receive AES-encrypted payloads. Clients must use <n-code>--encryption-key</n-code> to decrypt.</n-text>
          </n-form-item>
          <n-form-item v-if="form.encryption_mode === 'e2e'" label="Channel Keypair">
            <n-space vertical style="width: 100%;">
              <n-button @click="handleGenerateKeypair" secondary :loading="generatingKey">Generate Keypair</n-button>
              <template v-if="form.encryption_public_key">
                <n-text depth="3">Public Key:</n-text>
                <n-input-group>
                  <n-input :value="form.encryption_public_key" readonly />
                  <n-button @click="copyText(form.encryption_public_key!)">Copy</n-button>
                </n-input-group>
                <n-text depth="3">Private Key:</n-text>
                <n-input-group>
                  <n-input :value="form.encryption_private_key" readonly type="password" show-password-on="click" />
                  <n-button @click="copyText(form.encryption_private_key!)">Copy</n-button>
                </n-input-group>
                <n-button @click="downloadKeyFile" secondary>Download Key File</n-button>
              </template>
              <n-text v-else depth="3">No keypair generated yet.</n-text>
              <n-text depth="3" style="margin-top: 4px;">One shared keypair per channel. The public key is used by producers; the private key is distributed to clients.</n-text>
            </n-space>
          </n-form-item>
          <n-space>
            <n-button type="primary" @click="handleSave">Save</n-button>
            <n-button type="error" secondary @click="showDeleteModal = true">Delete Channel</n-button>
          </n-space>
        </n-form>
      </n-tab-pane>

      <n-tab-pane name="clients" tab="Clients">
        <n-space vertical style="max-width: 700px;">
          <n-h4>CLI Command Generator</n-h4>
          <template v-if="!form.encryption_mode || form.encryption_mode === 'none'">
            <n-code :code="clientCommandNoEnc" language="bash" />
            <n-space justify="end" style="margin-top: 4px;">
              <n-button size="tiny" @click="copyText(clientCommandNoEnc)">Copy</n-button>
            </n-space>
          </template>
          <template v-else-if="form.encryption_mode === 'server_side'">
            <n-text depth="3">Encryption Key (AES-256-GCM):</n-text>
            <n-space style="width: 100%;" align="center">
              <n-input :value="form.encryption_key" type="password" show-password-on="click" readonly style="flex: 1;" />
              <n-button size="small" @click="copyText(form.encryption_key)">Copy</n-button>
            </n-space>
            <n-text depth="3" style="margin-top: 8px;">Client command:</n-text>
            <n-code :code="clientCommandAES" language="bash" />
            <n-space justify="end" style="margin-top: 4px;">
              <n-button size="tiny" @click="copyText(clientCommandAES)">Copy</n-button>
            </n-space>
          </template>
          <template v-else-if="form.encryption_mode === 'e2e'">
            <n-h5>Producer</n-h5>
            <n-text depth="3" v-if="form.encryption_public_key">Public Key:</n-text>
            <n-space v-if="form.encryption_public_key" style="width: 100%;" align="center">
              <n-input :value="form.encryption_public_key" readonly style="flex: 1;" />
              <n-button size="small" @click="copyText(form.encryption_public_key!)">Copy</n-button>
            </n-space>
            <n-text v-else depth="3">Generate a keypair in the Settings tab first.</n-text>
            <template v-if="form.encryption_public_key">
              <n-text depth="3" style="margin-top: 8px;">Produce command (encrypt + send):</n-text>
              <n-code :code="produceCommand" language="bash" />
              <n-space justify="end" style="margin-top: 4px;">
                <n-button size="tiny" @click="copyText(produceCommand)">Copy</n-button>
              </n-space>
              <n-text depth="3" style="margin-top: 8px;">Proxy command (local encrypt proxy):</n-text>
              <n-code :code="proxyCommand" language="bash" />
              <n-space justify="end" style="margin-top: 4px;">
                <n-button size="tiny" @click="copyText(proxyCommand)">Copy</n-button>
              </n-space>
            </template>
            <n-h5 style="margin-top: 24px;">Client</n-h5>
            <n-button v-if="form.encryption_private_key" @click="downloadKeyFile" secondary>Download Key File</n-button>
            <n-text v-else depth="3">Generate a keypair in the Settings tab first.</n-text>
            <template v-if="form.encryption_private_key">
              <n-text depth="3" style="margin-top: 8px;">Client command:</n-text>
              <n-code :code="clientCommandE2E" language="bash" />
              <n-space justify="end" style="margin-top: 4px;">
                <n-button size="tiny" @click="copyText(clientCommandE2E)">Copy</n-button>
              </n-space>
            </template>
          </template>
        </n-space>
      </n-tab-pane>
    </n-tabs>

    <n-drawer v-model:show="showSendDrawer" :width="420" placement="right">
      <n-drawer-content title="Send Payload" closable>
        <n-space vertical>
          <n-alert v-if="!eventsStore.connected" type="warning" title="Not Connected" :bordered="false">
            SSE connection is not active. Connect to send test payloads.
          </n-alert>

          <n-card title="GitHub-style Payload" size="small">
            <n-space vertical>
              <n-form-item label="Repository">
                <n-input v-model:value="ghRepo" placeholder="owner/repo" />
              </n-form-item>
              <n-form-item label="Event Type">
                <n-select v-model:value="ghEvent" :options="githubEventOptions" />
              </n-form-item>
              <n-button @click="handleGenerateAndSend" :disabled="!eventsStore.connected || !ghRepo" block>
                Generate &amp; Send
              </n-button>
            </n-space>
          </n-card>

          <n-card title="Raw JSON Payload" size="small">
            <n-space vertical>
              <n-input
                v-model:value="rawPayload"
                type="textarea"
                rows="8"
                placeholder='{"example": "payload"}'
                :disabled="!eventsStore.connected"
              />
              <n-button @click="handleSendRaw" :disabled="!eventsStore.connected || !rawPayload.trim()" block>
                Send
              </n-button>
            </n-space>
          </n-card>
        </n-space>
      </n-drawer-content>
    </n-drawer>

    <n-modal v-model:show="showDeleteModal" title="Delete Channel" preset="card" style="width: 400px;">
      <n-space vertical>
        <n-text type="warning">Are you sure you want to delete channel '{{ channelId }}'? This action cannot be undone.</n-text>
        <n-form-item label="Type the channel ID to confirm">
          <n-input v-model:value="deleteConfirmInput" :placeholder="channelId" />
        </n-form-item>
        <n-space justify="end">
          <n-button @click="showDeleteModal = false">Cancel</n-button>
          <n-button type="error" :disabled="deleteConfirmInput !== channelId" :loading="deleting" @click="handleDelete">Delete</n-button>
        </n-space>
      </n-space>
    </n-modal>
  </n-spin>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NSpace, NSpin, NTabs, NTabPane, NForm, NFormItem, NInput, NInputNumber, NButton, NDynamicInput, NSelect, NCode, NText, NH4, NH5, NCard, NAlert, NDrawer, NDrawerContent, NInputGroup, type SelectOption } from 'naive-ui'
import { api } from '../api/client'
import type { Channel } from '../api/client'
import { useEventsStore } from '../stores/events'
import EventFeed from '../components/EventFeed.vue'
import { useMessage } from 'naive-ui'
import { bodySizeToBytes, bytesToBodySizeUnit, bodySizeUnitOptions, type BodySizeUnit } from '../utils/units'

const route = useRoute()
const router = useRouter()
const channelId = route.params.id as string
const eventsStore = useEventsStore()
const message = useMessage()

const loading = ref(true)
const channel = ref<Channel | null>(null)
const tab = ref('data')

const form = reactive({
  description: '',
  webhook_secret: '',
  allowed_ips: [''] as string[],
  max_body_size: 26214400,
  message_ttl_seconds: 0,
  encryption_mode: '',
  encryption_key: '',
  encryption_public_key: '',
  encryption_private_key: '',
})

const bodySizeUnit = ref<BodySizeUnit>('bytes')
const bodySizeUnits = bodySizeUnitOptions

const encryptionModeOptions: SelectOption[] = [
  { label: 'None', value: '' },
  { label: 'Server-side (AES-256-GCM)', value: 'server_side' },
  { label: 'End-to-end (NaCl box)', value: 'e2e' },
]

const origin = typeof window !== 'undefined' ? window.location.origin : ''

const ghRepo = ref('')
const ghEvent = ref('push')
const rawPayload = ref('')
const showSendDrawer = ref(false)
const showDeleteModal = ref(false)
const deleteConfirmInput = ref('')
const deleting = ref(false)

const githubEventOptions: SelectOption[] = [
  { label: 'push', value: 'push' },
  { label: 'pull_request', value: 'pull_request' },
  { label: 'issues', value: 'issues' },
  { label: 'release', value: 'release' },
  { label: 'ping', value: 'ping' },
  { label: 'create', value: 'create' },
  { label: 'delete', value: 'delete' },
  { label: 'deployment', value: 'deployment' },
  { label: 'deployment_status', value: 'deployment_status' },
  { label: 'fork', value: 'fork' },
  { label: 'gollum', value: 'gollum' },
  { label: 'issue_comment', value: 'issue_comment' },
  { label: 'label', value: 'label' },
  { label: 'member', value: 'member' },
  { label: 'milestone', value: 'milestone' },
  { label: 'page_build', value: 'page_build' },
  { label: 'public', value: 'public' },
  { label: 'pull_request_review', value: 'pull_request_review' },
  { label: 'pull_request_review_comment', value: 'pull_request_review_comment' },
  { label: 'push (tag)', value: 'tag_push' },
  { label: 'registry_package', value: 'registry_package' },
  { label: 'star', value: 'star' },
  { label: 'status', value: 'status' },
  { label: 'watch', value: 'watch' },
  { label: 'workflow_dispatch', value: 'workflow_dispatch' },
  { label: 'workflow_run', value: 'workflow_run' },
]

function buildGitHubPayload(repo: string, event: string): Record<string, any> {
  const parts = repo.split('/')
  const owner = parts[0] || 'test-owner'
  const name = parts[1] || 'test-repo'
  const now = new Date().toISOString()
  return {
    repository: {
      name,
      full_name: repo,
      owner: { login: owner, name: owner },
      html_url: `https://github.com/${repo}`,
      default_branch: 'main',
    },
    sender: { login: owner, id: 1 },
    ref: event === 'tag_push' ? 'refs/tags/v1.0.0' : 'refs/heads/main',
    commits: event === 'push' ? [{ id: 'abc123', message: 'test commit', timestamp: now, author: { name: owner }, committer: { name: owner } }] : undefined,
    action: ['pull_request', 'issues', 'issue_comment', 'pull_request_review', 'pull_request_review_comment'].includes(event) ? 'opened' : undefined,
    pull_request: event === 'pull_request' ? { number: 1, title: 'Test PR', state: 'open', body: 'Test body' } : undefined,
    issue: event === 'issues' ? { number: 1, title: 'Test Issue', state: 'open', body: 'Test body' } : undefined,
    release: event === 'release' ? { tag_name: 'v1.0.0', name: 'v1.0.0', body: 'Release notes', prerelease: false } : undefined,
    deployment: event === 'deployment' ? { sha: 'abc123', ref: 'main', environment: 'production' } : undefined,
    deployment_status: event === 'deployment_status' ? { state: 'success', environment: 'production' } : undefined,
    comment: ['issue_comment', 'pull_request_review_comment'].includes(event) ? { body: 'Test comment', user: { login: owner } } : undefined,
    review: event === 'pull_request_review' ? { state: 'approved', body: 'LGTM' } : undefined,
    forkee: event === 'fork' ? { name: 'forked-repo', owner: { login: 'forker' } } : undefined,
    label: event === 'label' ? { name: 'bug', color: 'd73a4a' } : undefined,
    member: event === 'member' ? { login: 'new-member' } : undefined,
    milestone: event === 'milestone' ? { title: 'v1.0', state: 'open' } : undefined,
    pages: event === 'page_build' ? [{ page_name: 'index', title: 'Home' }] : undefined,
    public: event === 'public' ? true : undefined,
    created: event === 'create' ? true : undefined,
    deleted: event === 'delete' ? true : undefined,
    zen: event === 'ping' ? 'Speak like a human' : undefined,
    hook_id: event === 'ping' ? 12345678 : undefined,
    workflow: event === 'workflow_dispatch' ? 'test-workflow.yml' : undefined,
    workflow_run: event === 'workflow_run' ? { workflow: 'CI', conclusion: 'success' } : undefined,
  }
}

async function handleGenerateAndSend() {
  if (!ghRepo.value) return
  const payload = buildGitHubPayload(ghRepo.value, ghEvent.value)
  try {
    await api.sendTestPayload(channelId, payload)
    message.success(`Sent ${ghEvent.value} event`)
  } catch (e: any) {
    message.error(e.message || 'Failed to send test payload')
  }
}

async function handleSendRaw() {
  if (!rawPayload.value.trim()) return
  try {
    const parsed = JSON.parse(rawPayload.value)
    await api.sendTestPayload(channelId, parsed)
    message.success('Raw payload sent')
  } catch (e: any) {
    if (e instanceof SyntaxError) {
      message.error('Invalid JSON')
    } else {
      message.error(e.message || 'Failed to send test payload')
    }
  }
}

const clientCommandNoEnc = computed(() =>
  `gohookbridge client ${origin}/${channelId} http://localhost:8080`
)

const clientCommandAES = computed(() =>
  form.encryption_key
    ? `gohookbridge client --encryption-key ${form.encryption_key} ${origin}/${channelId} http://localhost:8080`
    : ''
)

const keyFileName = computed(() => `gohookbridge-key-${channelId}.json`)

const clientCommandE2E = computed(() =>
  `gohookbridge client --encryption-key-file ./${keyFileName.value} ${origin}/${channelId} http://localhost:8080`
)

const produceCommand = computed(() =>
  form.encryption_public_key
    ? `gohookbridge produce --pubkey ${form.encryption_public_key} ${origin}/${channelId} payload.json`
    : ''
)

const proxyCommand = computed(() =>
  form.encryption_public_key
    ? `gohookbridge proxy --pubkey ${form.encryption_public_key} --listen :9090 --target ${origin}/${channelId}`
    : ''
)

const generatingKey = ref(false)

async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    message.success('Copied to clipboard')
  } catch {
    message.error('Failed to copy')
  }
}

onMounted(async () => {
  try {
    channel.value = await api.getChannel(channelId)
    form.description = channel.value.description || ''
    form.webhook_secret = channel.value.webhook_secret || ''
    form.allowed_ips = (channel.value.allowed_ips?.length ? channel.value.allowed_ips : [''])
    form.max_body_size = channel.value.max_body_size || 26214400
    form.message_ttl_seconds = channel.value.message_ttl_seconds || 0
    form.encryption_mode = channel.value.encryption_mode || ''
    form.encryption_key = channel.value.encryption_key || ''
    form.encryption_public_key = channel.value.encryption_public_key || ''
    form.encryption_private_key = channel.value.encryption_private_key || ''

    const bs = bytesToBodySizeUnit(form.max_body_size)
    form.max_body_size = Math.round(bs.value)
    bodySizeUnit.value = bs.unit

    eventsStore.connect(channelId)
  } catch (e: any) {
    message.error(e.message || 'Failed to load channel')
  } finally {
    loading.value = false
  }
})

onUnmounted(() => {
  eventsStore.disconnect()
})

async function handleSave() {
  const clean = (arr: string[]) => arr.filter(s => s.trim() !== '')
  const maxBodyBytes = bodySizeToBytes(form.max_body_size, bodySizeUnit.value)
  try {
    await api.updateChannel(channelId, {
      description: form.description || undefined,
      webhook_secret: form.webhook_secret || undefined,
      allowed_ips: clean(form.allowed_ips),
      max_body_size: maxBodyBytes,
      message_ttl_seconds: form.message_ttl_seconds || 0,
      encryption_mode: form.encryption_mode || undefined,
      encryption_key: form.encryption_key || undefined,
      encryption_public_key: form.encryption_mode === 'e2e' ? form.encryption_public_key || undefined : undefined,
      encryption_private_key: form.encryption_mode === 'e2e' ? form.encryption_private_key || undefined : undefined,
    })
    message.success('Saved')
  } catch (e: any) {
    message.error(e.message || 'Failed to save')
  }
}

async function handleGenerateSecret() {
  try {
    const result = await api.generateWebhookSecret(channelId)
    form.webhook_secret = result.webhook_secret
    message.success('Secret generated')
  } catch (e: any) {
    message.error(e.message || 'Failed to generate secret')
  }
}

async function handleGenerateEncryptionKey(mode: string) {
  try {
    const result = await api.generateEncryptionKey(channelId, mode)
    if (result.encryption_key) {
      form.encryption_key = result.encryption_key
    }
    form.encryption_mode = result.encryption_mode || mode
    message.success(`${mode} key generated`)
  } catch (e: any) {
    message.error(e.message || 'Failed to generate key')
  }
}

async function handleGenerateKeypair() {
  generatingKey.value = true
  try {
    const result = await api.generateEncryptionKey(channelId, 'e2e')
    form.encryption_mode = result.encryption_mode || 'e2e'
    if (result.encryption_public_key) {
      form.encryption_public_key = result.encryption_public_key
    }
    if (result.encryption_private_key) {
      form.encryption_private_key = result.encryption_private_key
    }
    if (result.key_file) {
      downloadBlob(result.key_file)
    }
    message.success('Keypair generated')
  } catch (e: any) {
    message.error(e.message || 'Failed to generate keypair')
  } finally {
    generatingKey.value = false
  }
}

function downloadKeyFile() {
  if (form.encryption_public_key && form.encryption_private_key) {
    const keyFile = {
      public_key: rawURLToStd(form.encryption_public_key),
      private_key: form.encryption_private_key,
    }
    downloadBlob(keyFile)
  }
}

function rawURLToStd(rawURL: string): string {
  const raw = rawURL.replace(/-/g, '+').replace(/_/g, '/')
  const padding = (4 - (raw.length % 4)) % 4
  const std = raw + '='.repeat(padding)
  return std
}

function downloadBlob(keyFile: { public_key: string; private_key: string }) {
  const blob = new Blob([JSON.stringify(keyFile, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = keyFileName.value
  a.click()
  URL.revokeObjectURL(url)
  message.success('Key file downloaded')
}

async function handleReplayEvent(eventId: string) {
  try {
    await api.replayEvent(channelId, eventId)
    message.success(`Event ${eventId.slice(0, 8)}... replayed`)
  } catch (e: any) {
    message.error(e.message || 'Failed to replay event')
  }
}

async function handleDelete() {
  deleting.value = true
  try {
    await api.deleteChannel(channelId)
    message.success('Channel deleted')
    router.push('/channels')
  } catch (e: any) {
    message.error(e.message || 'Failed to delete channel')
  } finally {
    deleting.value = false
    showDeleteModal.value = false
    deleteConfirmInput.value = ''
  }
}
</script>