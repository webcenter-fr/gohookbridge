<template>
  <div class="flex flex-col gap-4">
    <div class="flex items-center justify-between">
      <h3 class="text-lg font-semibold m-0">OIDC Providers</h3>
      <UButton color="primary" @click="openCreate">New Provider</UButton>
    </div>

    <UTable :data="providers" :columns="columns" :loading="loading">
      <template #actions-cell="{ row }">
        <div class="flex items-center gap-2">
          <UButton size="sm" color="neutral" variant="soft" @click="openEdit(row.original)">Edit</UButton>
          <UButton size="sm" color="error" variant="ghost" @click="confirmDelete(row.original.id)">Delete</UButton>
        </div>
      </template>
    </UTable>

    <UModal v-model:open="showModal" :title="editingId ? 'Edit Provider' : 'New Provider'">
      <template #body>
        <div class="flex flex-col gap-4">
          <UFormField label="ID">
            <UInput v-model="form.id" :disabled="!!editingId" class="w-full" />
          </UFormField>
          <UFormField label="Name">
            <UInput v-model="form.name" class="w-full" />
          </UFormField>
          <UFormField label="Client ID">
            <UInput v-model="form.client_id" class="w-full" />
          </UFormField>
          <UFormField label="Client Secret">
            <UInput v-model="form.client_secret" type="password" class="w-full" />
          </UFormField>
          <UFormField label="Issuer URL">
            <UInput v-model="form.issuer_url" placeholder="https://accounts.google.com" class="w-full" />
          </UFormField>
          <UFormField label="Scopes">
            <UInput v-model="form.scopesStr" placeholder="openid profile email" class="w-full" />
          </UFormField>
          <UFormField label="Groups Claim">
            <UInput v-model="form.groups_claim" placeholder="groups" class="w-full" />
            <template #hint>The OIDC claim name that contains group memberships (default: groups)</template>
          </UFormField>
        </div>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2 w-full">
          <UButton color="neutral" variant="ghost" @click="showModal = false">Cancel</UButton>
          <UButton color="primary" @click="handleSave">Save</UButton>
        </div>
      </template>
    </UModal>

    <UModal v-model:open="showDeleteModal" title="Delete Provider">
      <template #body>
        <p>Delete provider {{ deleteTarget }}?</p>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2 w-full">
          <UButton color="neutral" variant="ghost" @click="showDeleteModal = false">Cancel</UButton>
          <UButton color="error" @click="handleDelete">Delete</UButton>
        </div>
      </template>
    </UModal>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, reactive } from 'vue'
import type { TableColumn } from '@nuxt/ui'
import { api, type OIDCProvider } from '~/utils/api'

definePageMeta({ middleware: 'admin' })

const toast = useToast()

const providers = ref<OIDCProvider[]>([])
const loading = ref(true)
const showModal = ref(false)
const editingId = ref<string | null>(null)
const showDeleteModal = ref(false)
const deleteTarget = ref('')

const form = reactive({
  id: '',
  name: '',
  client_id: '',
  client_secret: '',
  issuer_url: '',
  scopesStr: 'openid profile email',
  groups_claim: 'groups',
})

const columns: TableColumn<OIDCProvider>[] = [
  { accessorKey: 'id', header: 'ID' },
  { accessorKey: 'name', header: 'Name' },
  { accessorKey: 'client_id', header: 'Client ID' },
  { accessorKey: 'issuer_url', header: 'Issuer URL' },
  { accessorKey: 'groups_claim', header: 'Groups Claim' },
  { id: 'actions', header: 'Actions' },
]

onMounted(async () => {
  await fetchProviders()
})

async function fetchProviders() {
  loading.value = true
  try {
    providers.value = await api.listOIDCProviders()
  } catch (e: any) {
    toast.add({ title: e.message, color: 'error' })
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
  form.groups_claim = 'groups'
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
  form.groups_claim = provider.groups_claim || 'groups'
  showModal.value = true
}

async function handleSave() {
  try {
    await api.updateOIDCProvider(form.id, {
      id: form.id,
      name: form.name,
      client_id: form.client_id,
      client_secret: form.client_secret,
      issuer_url: form.issuer_url,
      scopes: form.scopesStr.split(' ').filter(Boolean),
      groups_claim: form.groups_claim || 'groups',
    })
    toast.add({ title: 'OIDC provider saved', color: 'success' })
    showModal.value = false
    await fetchProviders()
  } catch (e: any) {
    toast.add({ title: e.message, color: 'error' })
  }
}

function confirmDelete(id: string) {
  deleteTarget.value = id
  showDeleteModal.value = true
}

async function handleDelete() {
  try {
    await api.deleteOIDCProvider(deleteTarget.value)
    toast.add({ title: 'OIDC provider deleted', color: 'success' })
    showDeleteModal.value = false
    await fetchProviders()
  } catch (e: any) {
    toast.add({ title: e.message, color: 'error' })
  }
}
</script>
