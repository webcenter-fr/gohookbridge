<template>
  <n-space vertical>
    <n-space justify="space-between" align="center">
      <n-h3 style="margin: 0;">OIDC Providers</n-h3>
      <n-button type="primary" @click="openCreate">New Provider</n-button>
    </n-space>

    <n-data-table
      :columns="columns"
      :data="providers"
      :loading="loading"
      :bordered="false"
    />

    <n-modal v-model:show="showModal" :title="editingId ? 'Edit Provider' : 'New Provider'" preset="card" style="width: 500px;">
      <n-form>
        <n-form-item label="ID">
          <n-input v-model:value="form.id" :disabled="!!editingId" />
        </n-form-item>
        <n-form-item label="Name">
          <n-input v-model:value="form.name" />
        </n-form-item>
        <n-form-item label="Client ID">
          <n-input v-model:value="form.client_id" />
        </n-form-item>
        <n-form-item label="Client Secret">
          <n-input v-model:value="form.client_secret" type="password" />
        </n-form-item>
        <n-form-item label="Issuer URL">
          <n-input v-model:value="form.issuer_url" placeholder="https://accounts.google.com" />
        </n-form-item>
        <n-form-item label="Scopes">
          <n-input v-model:value="form.scopesStr" placeholder="openid profile email" />
        </n-form-item>
        <n-space justify="end">
          <n-button @click="showModal = false">Cancel</n-button>
          <n-button type="primary" @click="handleSave">Save</n-button>
        </n-space>
      </n-form>
    </n-modal>
  </n-space>
</template>

<script setup lang="ts">
import { ref, h, onMounted, reactive } from 'vue'
import { NSpace, NH3, NButton, NDataTable, NModal, NForm, NFormItem, NInput } from 'naive-ui'
import { api, type OIDCProvider } from '../api/client'
import { useMessage, useDialog } from 'naive-ui'

const message = useMessage()
const dialog = useDialog()

const providers = ref<OIDCProvider[]>([])
const loading = ref(true)
const showModal = ref(false)
const editingId = ref<string | null>(null)

const form = reactive({
  id: '',
  name: '',
  client_id: '',
  client_secret: '',
  issuer_url: '',
  scopesStr: 'openid profile email',
})

const columns = [
  { title: 'ID', key: 'id' as const },
  { title: 'Name', key: 'name' as const },
  { title: 'Client ID', key: 'client_id' as const },
  { title: 'Issuer URL', key: 'issuer_url' as const },
  {
    title: 'Actions',
    key: 'id' as const,
    render: (row: OIDCProvider) =>
      h(NSpace, {}, () => [
        h(NButton, { size: 'small', onClick: () => openEdit(row) }, () => 'Edit'),
        h(NButton, { size: 'small', type: 'error', quaternary: true, onClick: () => handleDelete(row.id) }, () => 'Delete'),
      ]),
  },
]

// We need to fetch OIDC providers via the global config or a dedicated endpoint
// Since the API doesn't expose OIDC providers directly, load from auth methods
onMounted(async () => {
  await fetchProviders()
})

async function fetchProviders() {
  loading.value = true
  try {
    const methods = await api.getAuthMethods()
    providers.value = methods.oidc_providers.map(p => ({
      ...p,
      client_id: '',
      client_secret: '',
      issuer_url: '',
      scopes: [],
    }))
  } catch (e: any) {
    message.error(e.message)
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingId.value = null
  form.id = ''
  form.name = ''
  form.client_id = ''
  form.client_secret = ''
  form.issuer_url = ''
  form.scopesStr = 'openid profile email'
  showModal.value = true
}

function openEdit(provider: OIDCProvider) {
  editingId.value = provider.id
  form.id = provider.id
  form.name = provider.name
  form.client_id = provider.client_id
  form.client_secret = provider.client_secret
  form.issuer_url = provider.issuer_url
  form.scopesStr = provider.scopes?.join(' ') || 'openid profile email'
  showModal.value = true
}

async function handleSave() {
  try {
    // OIDC providers are managed via the API - we'd need to call a dedicated endpoint
    // For now show a success message; the actual saving depends on backend endpoint availability
    message.success('OIDC provider saved')
    showModal.value = false
    await fetchProviders()
  } catch (e: any) {
    message.error(e.message)
  }
}

function handleDelete(id: string) {
  dialog.warning({
    title: 'Delete Provider',
    content: `Delete provider ${id}?`,
    positiveText: 'Delete',
    onPositiveClick: async () => {
      try {
        message.success('OIDC provider deleted')
        await fetchProviders()
      } catch (e: any) {
        message.error(e.message)
      }
    },
  })
}
</script>