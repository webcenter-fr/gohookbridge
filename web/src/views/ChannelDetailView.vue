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
            <n-button @click="handleConnect" :disabled="eventsStore.connected">Connect</n-button>
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
          <n-card v-if="channel?.encryption_mode === 'e2e'" title="Client-Side Decryption" size="small" :bordered="true">
            <n-alert type="info" :bordered="false">
              <template #header>E2E Encrypted Channel</template>
              Events are encrypted with end-to-end encryption. Provide the private key to decrypt them in your browser. The key is never sent to the server.
            </n-alert>
            <n-form-item label="Private Key">
              <n-input
                v-model:value="privateKey"
                type="password"
                show-password-on="click"
                placeholder="Paste private key (base64)"
                @keyup.enter="eventsStore.connect(channelId, undefined, privateKey || undefined)"
              />
            </n-form-item>
            <n-button @click="eventsStore.connect(channelId, undefined, privateKey || undefined)" :disabled="!privateKey" secondary>
              Enable Decryption
            </n-button>
          </n-card>
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
              </template>
              <n-text v-else depth="3">No keypair generated yet.</n-text>
              <n-text depth="3" style="margin-top: 4px;">One shared keypair per channel. The public key is used by producers; the private key is distributed to clients.</n-text>
            </n-space>
          </n-form-item>

          <n-divider />

          <n-form-item label="Access Mode">
            <n-select v-model:value="form.access_mode" :options="accessModeOptions" @update:value="handleAccessModeChange" />
          </n-form-item>

          <template v-if="form.access_mode === 'token'">
            <n-form-item label="Access Tokens">
              <n-space vertical style="width: 100%;">
                <n-button @click="showCreateTokenModal = true" secondary type="primary">Generate Token</n-button>
                <n-data-table
                  v-if="accessTokens.length > 0"
                  :columns="tokenColumns"
                  :data="accessTokens"
                  :bordered="false"
                  :single-line="true"
                  size="small"
                />
                <n-text v-else depth="3">No access tokens created yet.</n-text>
              </n-space>
            </n-form-item>
          </template>

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
            <template v-if="form.encryption_public_key">
              <n-text depth="3" style="margin-top: 8px;">Client command:</n-text>
              <n-code :code="clientCommandE2E" language="bash" />
              <n-space justify="end" style="margin-top: 4px;">
                <n-button size="tiny" @click="copyText(clientCommandE2E)">Copy</n-button>
              </n-space>
            </template>
          </template>
        </n-space>
      </n-tab-pane>

      <n-tab-pane name="acl" tab="Access Control">
        <n-space vertical>
          <n-space justify="space-between" align="center">
            <n-h4 style="margin: 0;">Channel ACL</n-h4>
            <n-button v-if="canManageACL" type="primary" size="small" @click="showAclModal = true">Add Entry</n-button>
          </n-space>
          <n-data-table
            :columns="aclColumns"
            :data="aclEntries"
            :loading="aclLoading"
            :bordered="false"
          />
          <n-text v-if="!aclLoading && aclEntries.length === 0" depth="3">No ACL entries. Channel creator is the implicit owner.</n-text>

          <n-modal v-model:show="showAclModal" title="Add ACL Entry" preset="card" style="width: 450px;">
            <n-form>
              <n-form-item label="Type">
                <n-select v-model:value="aclForm.type" :options="aclTypeOptions" />
              </n-form-item>
              <n-form-item label="Subject">
                <n-input v-model:value="aclForm.subject" placeholder="username or group name" />
              </n-form-item>
              <n-form-item label="Role">
                <n-select v-model:value="aclForm.role" :options="aclRoleOptions" />
              </n-form-item>
              <n-space justify="end">
                <n-button @click="showAclModal = false">Cancel</n-button>
                <n-button type="primary" @click="handleAddAclEntry">Add</n-button>
              </n-space>
            </n-form>
          </n-modal>
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

    <n-modal v-model:show="showCreateTokenModal" title="Generate Access Token" preset="card" style="width: 450px;" :mask-closable="false">
      <n-space vertical>
        <n-form-item label="Token Name">
          <n-input v-model:value="newTokenName" placeholder="default" />
        </n-form-item>
        <n-form-item label="Scope">
          <n-select v-model:value="newTokenScope" :options="tokenScopeOptions" />
        </n-form-item>
        <n-space justify="end">
          <n-button @click="showCreateTokenModal = false">Cancel</n-button>
          <n-button type="primary" :loading="creatingToken" @click="handleCreateToken">Generate</n-button>
        </n-space>
      </n-space>
    </n-modal>

    <n-modal v-model:show="showTokenResultModal" title="Access Token Created" preset="card" style="width: 500px;" :mask-closable="false">
      <n-space vertical>
        <n-alert type="warning" :bordered="false">
          <template #header>Save this token now</template>
          The token is shown only once. If you lose it, you will need to generate a new one.
        </n-alert>
        <n-input-group>
          <n-input :value="createdTokenRaw" readonly />
          <n-button @click="copyText(createdTokenRaw)">Copy</n-button>
        </n-input-group>
        <n-tag :type="tokenScopeTagType(createdTokenScope)" size="small">{{ createdTokenScope }}</n-tag>
        <n-space justify="end">
          <n-button type="primary" @click="handleCloseTokenResult">Done</n-button>
        </n-space>
      </n-space>
    </n-modal>
  </n-spin>
</template>

<script setup lang="ts">
import { ref, reactive, computed, h, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NSpace, NSpin, NTabs, NTabPane, NForm, NFormItem, NInput, NInputNumber, NButton, NDynamicInput, NSelect, NCode, NText, NH4, NH5, NCard, NAlert, NDrawer, NDrawerContent, NInputGroup, NDataTable, NDivider, NTag, type SelectOption, type DataTableColumn } from 'naive-ui'
import { api } from '../api/client'
import type { Channel } from '../api/client'
import { useEventsStore } from '../stores/events'
import EventFeed from '../components/EventFeed.vue'
import { useMessage } from 'naive-ui'
import { bodySizeToBytes, bytesToBodySizeUnit, bodySizeUnitOptions, type BodySizeUnit } from '../utils/units'
import { generateKeyPair } from '../utils/crypto'

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
  access_mode: 'public',
})

const accessTokens = ref<{ id: string; name: string; scope: string; created_at: string }[]>([])

const aclEntries = ref<{ id: string; channel_id: string; type: string; subject: string; role: string }[]>([])
const aclLoading = ref(false)
const showAclModal = ref(false)
const userPermissions = ref<string[]>([])
const canManageACL = computed(() =>
  userPermissions.value.includes('channel:write') ||
  userPermissions.value.includes('rbac:write') ||
  userPermissions.value.includes('*')
)
const aclForm = reactive({
  type: 'user',
  subject: '',
  role: 'read',
})
const aclTypeOptions = [
  { label: 'User', value: 'user' },
  { label: 'Group', value: 'group' },
]
const aclRoleOptions = [
  { label: 'Owner', value: 'owner' },
  { label: 'Write', value: 'write' },
  { label: 'Read', value: 'read' },
]
const aclColumns = computed(() => {
  const cols: any[] = [
    { title: 'Type', key: 'type' as const },
    { title: 'Subject', key: 'subject' as const },
    { title: 'Role', key: 'role' as const },
  ]
  if (canManageACL.value) {
    cols.push({
      title: 'Actions',
      key: 'id' as const,
      render: (row: { id: string }) =>
        h(NButton, { size: 'tiny', type: 'error', secondary: true, onClick: () => handleDeleteAclEntry(row.id) }, () => 'Delete'),
    })
  }
  return cols
})

const showCreateTokenModal = ref(false)
const showTokenResultModal = ref(false)
const newTokenName = ref('')
const newTokenScope = ref('both')
const creatingToken = ref(false)
const createdTokenRaw = ref('')
const createdTokenScope = ref('')

const accessModeOptions: SelectOption[] = [
  { label: 'Public', value: 'public' },
  { label: 'Token required', value: 'token' },
]

const tokenScopeOptions: SelectOption[] = [
  { label: 'Produce', value: 'produce' },
  { label: 'Consume', value: 'consume' },
  { label: 'Both', value: 'both' },
]

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

const clientCommandNoEnc = computed(() => {
  let cmd = `gohookbridge client ${origin}/${channelId} http://localhost:8080`
  if (form.access_mode === 'token') {
    const t = findConsumeToken()
    if (t) cmd += ` --token <${t.name || t.id}_token>`
  }
  return cmd
})

const clientCommandAES = computed(() => {
  if (!form.encryption_key) return ''
  let cmd = `gohookbridge client --encryption-key ${form.encryption_key} ${origin}/${channelId} http://localhost:8080`
  if (form.access_mode === 'token') {
    const t = findConsumeToken()
    if (t) cmd += ` --token <${t.name || t.id}_token>`
  }
  return cmd
})

const keyFileName = computed(() => `gohookbridge-key-${channelId}.json`)

const clientCommandE2E = computed(() => {
  let cmd = `gohookbridge client --encryption-key-file ./${keyFileName.value} ${origin}/${channelId} http://localhost:8080`
  if (form.access_mode === 'token') {
    const t = findConsumeToken()
    if (t) cmd += ` --token <${t.name || t.id}_token>`
  }
  return cmd
})

const produceCommand = computed(() => {
  if (!form.encryption_public_key) return ''
  let cmd = `gohookbridge produce --pubkey ${form.encryption_public_key} ${origin}/${channelId} payload.json`
  if (form.access_mode === 'token') {
    const t = findProduceToken()
    if (t) cmd += ` --token <${t.name || t.id}_token>`
  }
  return cmd
})

const proxyCommand = computed(() => {
  if (!form.encryption_public_key) return ''
  let cmd = `gohookbridge proxy --pubkey ${form.encryption_public_key} --listen :9090 --target ${origin}/${channelId}`
  if (form.access_mode === 'token') {
    const t = findProduceToken()
    if (t) cmd += ` --token <${t.name || t.id}_token>`
  }
  return cmd
})

const generatingKey = ref(false)

const tokenColumns: DataTableColumn<{ id: string; name: string; scope: string; created_at: string }>[] = [
  { title: 'Name', key: 'name' },
  {
    title: 'Scope',
    key: 'scope',
    render(row) {
      const type = row.scope === 'produce' ? 'info' : row.scope === 'consume' ? 'success' : 'warning'
      return h(NTag, { type, size: 'small' }, { default: () => row.scope })
    },
  },
  { title: 'Created', key: 'created_at' },
  {
    title: 'Actions',
    key: 'actions',
    render(row) {
      return h(NButton, { size: 'tiny', type: 'error', secondary: true, onClick: () => handleDeleteToken(row.id) }, { default: () => 'Delete' })
    },
  },
]

function tokenScopeTagType(scope: string): 'info' | 'success' | 'warning' {
  return scope === 'produce' ? 'info' : scope === 'consume' ? 'success' : 'warning'
}

function findConsumeToken() {
  return accessTokens.value.find(t => t.scope === 'consume' || t.scope === 'both')
}

function findProduceToken() {
  return accessTokens.value.find(t => t.scope === 'produce' || t.scope === 'both')
}

async function handleCreateToken() {
  creatingToken.value = true
  try {
    const result = await api.createAccessToken(channelId, newTokenName.value || 'default', newTokenScope.value)
    createdTokenRaw.value = result.token
    createdTokenScope.value = result.scope
    showCreateTokenModal.value = false
    showTokenResultModal.value = true
    await loadAccessTokens()
  } catch (e: any) {
    message.error(e.message || 'Failed to create token')
  } finally {
    creatingToken.value = false
  }
}

function handleCloseTokenResult() {
  showTokenResultModal.value = false
  createdTokenRaw.value = ''
  newTokenName.value = ''
  newTokenScope.value = 'both'
}

async function handleDeleteToken(tokenId: string) {
  try {
    await api.deleteAccessToken(channelId, tokenId)
    await loadAccessTokens()
    message.success('Token deleted')
  } catch (e: any) {
    message.error(e.message || 'Failed to delete token')
  }
}

async function handleAccessModeChange(value: string) {
  try {
    await api.updateAccessMode(channelId, value)
    message.success(`Access mode set to ${value}`)
  } catch (e: any) {
    message.error(e.message || 'Failed to update access mode')
  }
}

async function loadAccessTokens() {
  try {
    const result = await api.listAccessTokens(channelId)
    accessTokens.value = result.tokens
    form.access_mode = result.access_mode
  } catch (e: any) {
    message.error(e.message || 'Failed to load access tokens')
    accessTokens.value = []
  }
}

async function loadAcl() {
  aclLoading.value = true
  try {
    aclEntries.value = (await api.listChannelACL(channelId)) || []
  } catch (e: any) {
    message.error(e.message || 'Failed to load ACL')
    aclEntries.value = []
  } finally {
    aclLoading.value = false
  }
}

async function handleAddAclEntry() {
  if (!aclForm.subject) {
    message.error('Subject is required')
    return
  }
  try {
    await api.addChannelACLEntry(channelId, {
      type: aclForm.type,
      subject: aclForm.subject,
      role: aclForm.role,
    })
    message.success('ACL entry added')
    showAclModal.value = false
    aclForm.subject = ''
    await loadAcl()
  } catch (e: any) {
    message.error(e.message || 'Failed to add ACL entry')
  }
}

async function handleDeleteAclEntry(entryId: string) {
  try {
    await api.deleteChannelACLEntry(channelId, entryId)
    message.success('ACL entry deleted')
    await loadAcl()
  } catch (e: any) {
    message.error(e.message || 'Failed to delete ACL entry')
  }
}

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
    const me = await api.getMe()
    userPermissions.value = me.permissions || []

    channel.value = await api.getChannel(channelId)
    form.description = channel.value.description || ''
    form.webhook_secret = channel.value.webhook_secret || ''
    form.allowed_ips = (channel.value.allowed_ips?.length ? channel.value.allowed_ips : [''])
    form.max_body_size = channel.value.max_body_size || 26214400
    form.message_ttl_seconds = channel.value.message_ttl_seconds || 0
    form.encryption_mode = channel.value.encryption_mode || ''
    form.encryption_key = channel.value.encryption_key || ''
    form.encryption_public_key = channel.value.encryption_public_key || ''
    form.access_mode = channel.value.access_mode || 'public'

    await Promise.all([loadAccessTokens(), loadAcl()])

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

const privateKey = ref('')

function handleConnect() {
  eventsStore.connect(channelId, undefined, privateKey.value || undefined)
}

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
    const kp = generateKeyPair()
    form.encryption_mode = 'e2e'
    form.encryption_public_key = kp.publicKey
    await api.updateChannel(channelId, {
      encryption_mode: 'e2e',
      encryption_public_key: kp.publicKey,
    })
    downloadBlob(kp.keyFile)
    message.success('Keypair generated')
  } catch (e: any) {
    message.error(e.message || 'Failed to generate keypair')
  } finally {
    generatingKey.value = false
  }
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