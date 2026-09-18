<template>
  <div :class="{ 'opacity-50 pointer-events-none': loading }">
    <UTabs v-model="tab" :items="tabItems">
      <template #data>
        <div class="flex flex-col gap-4">
          <EventFeed
            :channel="channelId"
            :events="eventsStore.events"
            :connected="eventsStore.connected"
            :connecting="eventsStore.connecting"
            :message-ttl="channel?.message_ttl_seconds"
            :encryption-mode="channel?.encryption_mode"
            @replay="handleReplayEvent"
          />
          <div class="flex items-center gap-2">
            <UButton color="neutral" variant="soft" :disabled="eventsStore.connected" @click="handleConnect">Connect</UButton>
            <UButton color="neutral" variant="soft" :disabled="!eventsStore.connected" @click="eventsStore.disconnect()">Disconnect</UButton>
            <UButton color="info" variant="soft" @click="showSendDrawer = true">Send Payload</UButton>
            <UButton color="neutral" variant="ghost" @click="eventsStore.clear()">Clear</UButton>
          </div>
        </div>
      </template>

      <template #settings>
        <form v-if="channel" class="flex flex-col gap-4 max-w-[600px]" @submit.prevent="handleSave">
          <UFormField label="Channel ID">
            <UInput :model-value="channel.id" disabled class="w-full" />
          </UFormField>
          <UFormField label="Description">
            <UTextarea v-model="form.description" :maxlength="500" class="w-full" />
          </UFormField>
          <UFormField label="Webhook Secret">
            <UFieldGroup class="w-full">
              <UInput v-model="form.webhook_secret" type="password" placeholder="webhook secret" class="flex-1" />
              <UButton color="neutral" variant="soft" @click="handleGenerateSecret">Generate</UButton>
            </UFieldGroup>
          </UFormField>
          <UFormField label="Allowed IPs">
            <div class="flex flex-col gap-2 w-full">
              <div v-for="(ip, i) in form.allowed_ips" :key="i" class="flex items-center gap-2">
                <UInput v-model="form.allowed_ips[i]" placeholder="10.0.0.0/8" class="flex-1" />
                <UButton icon="i-lucide-trash" color="error" variant="ghost" @click="form.allowed_ips.splice(i, 1)" />
              </div>
              <UButton icon="i-lucide-plus" color="neutral" variant="soft" class="self-start" @click="form.allowed_ips.push('')">Add IP</UButton>
            </div>
          </UFormField>
          <UFormField label="Max Body Size">
            <div class="flex items-center gap-2">
              <UInputNumber v-model="form.max_body_size" :min="0" class="w-[180px]" />
              <USelect v-model="bodySizeUnit" :items="bodySizeUnits" class="w-[100px]" />
            </div>
          </UFormField>
          <UCard v-if="channel?.encryption_mode === 'e2e'" title="Client-Side Decryption" variant="soft">
            <UAlert color="info" title="E2E Encrypted Channel">
              <template #description>
                Events are encrypted with end-to-end encryption. Provide the private key to decrypt them in your browser. The key is never sent to the server.
              </template>
            </UAlert>
            <UFormField label="Private Key" class="mt-4">
              <UInput
                v-model="privateKey"
                type="password"
                placeholder="Paste private key (base64)"
                class="w-full"
                @keyup.enter="eventsStore.connect(channelId, undefined, privateKey || undefined)"
              />
            </UFormField>
            <UButton
              color="neutral"
              variant="soft"
              class="mt-2"
              :disabled="!privateKey"
              @click="eventsStore.connect(channelId, undefined, privateKey || undefined)"
            >
              Enable Decryption
            </UButton>
          </UCard>
          <UFormField label="Message TTL (seconds)">
            <UInputNumber v-model="form.message_ttl_seconds" :min="0" placeholder="0 = use global default" />
          </UFormField>
          <UFormField label="Encryption Mode">
            <USelect v-model="form.encryption_mode" :items="encryptionModeOptions" class="w-full" />
          </UFormField>
          <UFormField v-if="form.encryption_mode === 'server_side'" label="Encryption Key">
            <UFieldGroup class="w-full">
              <UInput v-model="form.encryption_key" type="password" placeholder="AES-256 key (base64)" class="flex-1" />
              <UButton color="neutral" variant="soft" @click="handleGenerateEncryptionKey('server_side')">Generate Key</UButton>
            </UFieldGroup>
            <p class="text-sm text-(--ui-text-muted) mt-1">
              All subscribers receive AES-encrypted payloads. Clients must use <code>--encryption-key</code> to decrypt.
            </p>
          </UFormField>
          <UFormField v-if="form.encryption_mode === 'e2e'" label="Channel Keypair">
            <div class="flex flex-col gap-2 w-full">
              <UButton color="neutral" variant="soft" class="self-start" :loading="generatingKey" @click="handleGenerateKeypair">Generate Keypair</UButton>
              <template v-if="form.encryption_public_key">
                <p class="text-sm text-(--ui-text-muted)">Public Key:</p>
                <UFieldGroup class="w-full">
                  <UInput :model-value="form.encryption_public_key" readonly class="flex-1" />
                  <UButton color="neutral" variant="soft" @click="copyText(form.encryption_public_key!)">Copy</UButton>
                </UFieldGroup>
              </template>
              <p v-else class="text-sm text-(--ui-text-muted)">No keypair generated yet.</p>
              <p class="text-sm text-(--ui-text-muted) mt-1">
                One shared keypair per channel. The public key is used by producers; the private key is distributed to clients.
              </p>
            </div>
          </UFormField>

          <USeparator />

          <UFormField label="Access Mode">
            <USelect v-model="form.access_mode" :items="accessModeOptions" class="w-full" @update:model-value="handleAccessModeChange" />
          </UFormField>

          <template v-if="form.access_mode === 'token'">
            <UFormField label="Access Tokens">
              <div class="flex flex-col gap-2 w-full">
                <UButton color="primary" variant="soft" class="self-start" @click="showCreateTokenModal = true">Generate Token</UButton>
                <UTable v-if="accessTokens.length > 0" :data="accessTokens" :columns="tokenColumns">
                  <template #scope-cell="{ row }">
                    <UBadge :color="tokenScopeColor(row.original.scope)" size="sm">{{ row.original.scope }}</UBadge>
                  </template>
                  <template #actions-cell="{ row }">
                    <UButton size="xs" color="error" variant="soft" @click="handleDeleteToken(row.original.id)">Delete</UButton>
                  </template>
                </UTable>
                <p v-else class="text-sm text-(--ui-text-muted)">No access tokens created yet.</p>
              </div>
            </UFormField>
          </template>

          <div class="flex items-center gap-2">
            <UButton type="submit" color="primary">Save</UButton>
            <UButton color="error" variant="soft" @click="showDeleteModal = true">Delete Channel</UButton>
          </div>
        </form>
      </template>

      <template #clients>
        <div class="flex flex-col gap-3 max-w-[700px]">
          <h4 class="text-base font-semibold m-0">CLI Command Generator</h4>
          <template v-if="!form.encryption_mode || form.encryption_mode === 'none'">
            <AppCodeBlock :code="clientCommandNoEnc" language="bash" />
            <div class="flex justify-end">
              <UButton size="xs" color="neutral" variant="soft" @click="copyText(clientCommandNoEnc)">Copy</UButton>
            </div>
          </template>
          <template v-else-if="form.encryption_mode === 'server_side'">
            <p class="text-sm text-(--ui-text-muted)">Encryption Key (AES-256-GCM):</p>
            <div class="flex items-center gap-2 w-full">
              <UInput :model-value="form.encryption_key" type="password" readonly class="flex-1" />
              <UButton size="sm" color="neutral" variant="soft" @click="copyText(form.encryption_key)">Copy</UButton>
            </div>
            <p class="text-sm text-(--ui-text-muted) mt-2">Client command:</p>
            <AppCodeBlock :code="clientCommandAES" language="bash" />
            <div class="flex justify-end">
              <UButton size="xs" color="neutral" variant="soft" @click="copyText(clientCommandAES)">Copy</UButton>
            </div>
          </template>
          <template v-else-if="form.encryption_mode === 'e2e'">
            <h5 class="text-sm font-semibold m-0">Producer</h5>
            <p v-if="form.encryption_public_key" class="text-sm text-(--ui-text-muted)">Public Key:</p>
            <div v-if="form.encryption_public_key" class="flex items-center gap-2 w-full">
              <UInput :model-value="form.encryption_public_key" readonly class="flex-1" />
              <UButton size="sm" color="neutral" variant="soft" @click="copyText(form.encryption_public_key!)">Copy</UButton>
            </div>
            <p v-else class="text-sm text-(--ui-text-muted)">Generate a keypair in the Settings tab first.</p>
            <template v-if="form.encryption_public_key">
              <p class="text-sm text-(--ui-text-muted) mt-2">Produce command (encrypt + send):</p>
              <AppCodeBlock :code="produceCommand" language="bash" />
              <div class="flex justify-end">
                <UButton size="xs" color="neutral" variant="soft" @click="copyText(produceCommand)">Copy</UButton>
              </div>
              <p class="text-sm text-(--ui-text-muted) mt-2">Proxy command (local encrypt proxy):</p>
              <AppCodeBlock :code="proxyCommand" language="bash" />
              <div class="flex justify-end">
                <UButton size="xs" color="neutral" variant="soft" @click="copyText(proxyCommand)">Copy</UButton>
              </div>
            </template>
            <h5 class="text-sm font-semibold mt-6 m-0">Client</h5>
            <template v-if="form.encryption_public_key">
              <p class="text-sm text-(--ui-text-muted) mt-2">Client command:</p>
              <AppCodeBlock :code="clientCommandE2E" language="bash" />
              <div class="flex justify-end">
                <UButton size="xs" color="neutral" variant="soft" @click="copyText(clientCommandE2E)">Copy</UButton>
              </div>
            </template>
          </template>
        </div>
      </template>

      <template #acl>
        <div class="flex flex-col gap-4">
          <div class="flex items-center justify-between">
            <h4 class="text-base font-semibold m-0">Channel ACL</h4>
            <UButton v-if="canManageACL" color="primary" size="sm" @click="showAclModal = true">Add Entry</UButton>
          </div>
          <UTable :data="aclEntries" :columns="aclColumns" :loading="aclLoading">
            <template #actions-cell="{ row }">
              <UButton size="xs" color="error" variant="soft" @click="handleDeleteAclEntry(row.original.id)">Delete</UButton>
            </template>
          </UTable>
          <p v-if="!aclLoading && aclEntries.length === 0" class="text-sm text-(--ui-text-muted)">
            No ACL entries. Channel creator is the implicit owner.
          </p>
        </div>
      </template>
    </UTabs>

    <USlideover v-model:open="showSendDrawer" title="Send Payload" side="right">
      <template #body>
        <div class="flex flex-col gap-4">
          <UAlert v-if="!eventsStore.connected" color="warning" title="Not Connected">
            <template #description>SSE connection is not active. Connect to send test payloads.</template>
          </UAlert>

          <UCard title="GitHub-style Payload" variant="soft">
            <div class="flex flex-col gap-3">
              <UFormField label="Repository">
                <UInput v-model="ghRepo" placeholder="owner/repo" class="w-full" />
              </UFormField>
              <UFormField label="Event Type">
                <USelect v-model="ghEvent" :items="githubEventOptions" class="w-full" />
              </UFormField>
              <UButton block :disabled="!eventsStore.connected || !ghRepo" @click="handleGenerateAndSend">
                Generate &amp; Send
              </UButton>
            </div>
          </UCard>

          <UCard title="Raw JSON Payload" variant="soft">
            <div class="flex flex-col gap-3">
              <UTextarea
                v-model="rawPayload"
                :rows="8"
                placeholder='{"example": "payload"}'
                :disabled="!eventsStore.connected"
                class="w-full"
              />
              <UButton block :disabled="!eventsStore.connected || !rawPayload.trim()" @click="handleSendRaw">
                Send
              </UButton>
            </div>
          </UCard>
        </div>
      </template>
    </USlideover>

    <UModal v-model:open="showDeleteModal" title="Delete Channel">
      <template #body>
        <div class="flex flex-col gap-4">
          <p class="text-(--ui-text-warning)">Are you sure you want to delete channel '{{ channelId }}'? This action cannot be undone.</p>
          <UFormField label="Type the channel ID to confirm">
            <UInput v-model="deleteConfirmInput" :placeholder="channelId" class="w-full" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2 w-full">
          <UButton color="neutral" variant="ghost" @click="showDeleteModal = false">Cancel</UButton>
          <UButton color="error" :disabled="deleteConfirmInput !== channelId" :loading="deleting" @click="handleDelete">Delete</UButton>
        </div>
      </template>
    </UModal>

    <UModal v-model:open="showCreateTokenModal" title="Generate Access Token" :dismissible="false">
      <template #body>
        <div class="flex flex-col gap-4">
          <UFormField label="Token Name">
            <UInput v-model="newTokenName" placeholder="default" class="w-full" />
          </UFormField>
          <UFormField label="Scope">
            <USelect v-model="newTokenScope" :items="tokenScopeOptions" class="w-full" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2 w-full">
          <UButton color="neutral" variant="ghost" @click="showCreateTokenModal = false">Cancel</UButton>
          <UButton color="primary" :loading="creatingToken" @click="handleCreateToken">Generate</UButton>
        </div>
      </template>
    </UModal>

    <UModal v-model:open="showTokenResultModal" title="Access Token Created" :dismissible="false">
      <template #body>
        <div class="flex flex-col gap-4">
          <UAlert color="warning" title="Save this token now">
            <template #description>The token is shown only once. If you lose it, you will need to generate a new one.</template>
          </UAlert>
          <UFieldGroup class="w-full">
            <UInput :model-value="createdTokenRaw" readonly class="flex-1" />
            <UButton color="neutral" variant="soft" @click="copyText(createdTokenRaw)">Copy</UButton>
          </UFieldGroup>
          <UBadge :color="tokenScopeColor(createdTokenScope)" size="sm" class="self-start">{{ createdTokenScope }}</UBadge>
        </div>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2 w-full">
          <UButton color="primary" @click="handleCloseTokenResult">Done</UButton>
        </div>
      </template>
    </UModal>

    <UModal v-model:open="showAclModal" title="Add ACL Entry">
      <template #body>
        <div class="flex flex-col gap-4">
          <UFormField label="Type">
            <USelect v-model="aclForm.type" :items="aclTypeOptions" class="w-full" />
          </UFormField>
          <UFormField label="Subject">
            <UInput v-model="aclForm.subject" placeholder="username or group name" class="w-full" />
          </UFormField>
          <UFormField label="Role">
            <USelect v-model="aclForm.role" :items="aclRoleOptions" class="w-full" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2 w-full">
          <UButton color="neutral" variant="ghost" @click="showAclModal = false">Cancel</UButton>
          <UButton color="primary" @click="handleAddAclEntry">Add</UButton>
        </div>
      </template>
    </UModal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import type { TableColumn } from '@nuxt/ui'
import { api, type Channel } from '~/utils/api'
import { useEventsStore } from '~/stores/events'
import { bodySizeToBytes, bytesToBodySizeUnit, bodySizeUnitOptions, type BodySizeUnit } from '~/utils/units'
import { generateKeyPair } from '~/utils/crypto'

const route = useRoute()
const channelId = route.params.id as string
const eventsStore = useEventsStore()
const toast = useToast()

const loading = ref(true)
const channel = ref<Channel | null>(null)
const tab = ref('data')

const tabItems = [
  { label: 'Data', value: 'data', slot: 'data' as const },
  { label: 'Settings', value: 'settings', slot: 'settings' as const },
  { label: 'Clients', value: 'clients', slot: 'clients' as const },
  { label: 'Access Control', value: 'acl', slot: 'acl' as const },
]

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
const aclColumns = computed<TableColumn<{ id: string; channel_id: string; type: string; subject: string; role: string }>[]>(() => {
  const cols: TableColumn<{ id: string; channel_id: string; type: string; subject: string; role: string }>[] = [
    { accessorKey: 'type', header: 'Type' },
    { accessorKey: 'subject', header: 'Subject' },
    { accessorKey: 'role', header: 'Role' },
  ]
  if (canManageACL.value) {
    cols.push({ id: 'actions', header: 'Actions' })
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

const accessModeOptions = [
  { label: 'Public', value: 'public' },
  { label: 'Token required', value: 'token' },
]

const tokenScopeOptions = [
  { label: 'Produce', value: 'produce' },
  { label: 'Consume', value: 'consume' },
  { label: 'Both', value: 'both' },
]

const bodySizeUnit = ref<BodySizeUnit>('bytes')
const bodySizeUnits = bodySizeUnitOptions

const encryptionModeOptions = [
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

const githubEventOptions = [
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
    toast.add({ title: `Sent ${ghEvent.value} event`, color: 'success' })
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to send test payload', color: 'error' })
  }
}

async function handleSendRaw() {
  if (!rawPayload.value.trim()) return
  try {
    const parsed = JSON.parse(rawPayload.value)
    await api.sendTestPayload(channelId, parsed)
    toast.add({ title: 'Raw payload sent', color: 'success' })
  } catch (e: any) {
    if (e instanceof SyntaxError) {
      toast.add({ title: 'Invalid JSON', color: 'error' })
    } else {
      toast.add({ title: e.message || 'Failed to send test payload', color: 'error' })
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

const tokenColumns: TableColumn<{ id: string; name: string; scope: string; created_at: string }>[] = [
  { accessorKey: 'name', header: 'Name' },
  { id: 'scope', header: 'Scope' },
  { accessorKey: 'created_at', header: 'Created' },
  { id: 'actions', header: 'Actions' },
]

function tokenScopeColor(scope: string): 'info' | 'success' | 'warning' {
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
    toast.add({ title: e.message || 'Failed to create token', color: 'error' })
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
    toast.add({ title: 'Token deleted', color: 'success' })
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to delete token', color: 'error' })
  }
}

async function handleAccessModeChange(value: string | number) {
  try {
    await api.updateAccessMode(channelId, String(value))
    toast.add({ title: `Access mode set to ${value}`, color: 'success' })
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to update access mode', color: 'error' })
  }
}

async function loadAccessTokens() {
  try {
    const result = await api.listAccessTokens(channelId)
    accessTokens.value = result.tokens
    form.access_mode = result.access_mode
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to load access tokens', color: 'error' })
    accessTokens.value = []
  }
}

async function loadAcl() {
  aclLoading.value = true
  try {
    aclEntries.value = (await api.listChannelACL(channelId)) || []
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to load ACL', color: 'error' })
    aclEntries.value = []
  } finally {
    aclLoading.value = false
  }
}

async function handleAddAclEntry() {
  if (!aclForm.subject) {
    toast.add({ title: 'Subject is required', color: 'error' })
    return
  }
  try {
    await api.addChannelACLEntry(channelId, {
      type: aclForm.type,
      subject: aclForm.subject,
      role: aclForm.role,
    })
    toast.add({ title: 'ACL entry added', color: 'success' })
    showAclModal.value = false
    aclForm.subject = ''
    await loadAcl()
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to add ACL entry', color: 'error' })
  }
}

async function handleDeleteAclEntry(entryId: string) {
  try {
    await api.deleteChannelACLEntry(channelId, entryId)
    toast.add({ title: 'ACL entry deleted', color: 'success' })
    await loadAcl()
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to delete ACL entry', color: 'error' })
  }
}

async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast.add({ title: 'Copied to clipboard', color: 'success' })
  } catch {
    toast.add({ title: 'Failed to copy', color: 'error' })
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
    toast.add({ title: e.message || 'Failed to load channel', color: 'error' })
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
    toast.add({ title: 'Saved', color: 'success' })
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to save', color: 'error' })
  }
}

async function handleGenerateSecret() {
  try {
    const result = await api.generateWebhookSecret(channelId)
    form.webhook_secret = result.webhook_secret
    toast.add({ title: 'Secret generated', color: 'success' })
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to generate secret', color: 'error' })
  }
}

async function handleGenerateEncryptionKey(mode: string) {
  try {
    const result = await api.generateEncryptionKey(channelId, mode)
    if (result.encryption_key) {
      form.encryption_key = result.encryption_key
    }
    form.encryption_mode = result.encryption_mode || mode
    toast.add({ title: `${mode} key generated`, color: 'success' })
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to generate key', color: 'error' })
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
    toast.add({ title: 'Keypair generated', color: 'success' })
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to generate keypair', color: 'error' })
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
  toast.add({ title: 'Key file downloaded', color: 'success' })
}

async function handleReplayEvent(eventId: string) {
  try {
    await api.replayEvent(channelId, eventId)
    toast.add({ title: `Event ${eventId.slice(0, 8)}... replayed`, color: 'success' })
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to replay event', color: 'error' })
  }
}

async function handleDelete() {
  deleting.value = true
  try {
    await api.deleteChannel(channelId)
    toast.add({ title: 'Channel deleted', color: 'success' })
    await navigateTo('/channels')
  } catch (e: any) {
    toast.add({ title: e.message || 'Failed to delete channel', color: 'error' })
  } finally {
    deleting.value = false
    showDeleteModal.value = false
    deleteConfirmInput.value = ''
  }
}
</script>
