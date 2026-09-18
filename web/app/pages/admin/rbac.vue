<template>
  <div class="flex flex-col gap-4">
    <h3 class="text-lg font-semibold m-0">Role Bindings</h3>

    <UTable :data="bindings" :columns="bindingColumns" :loading="loading">
      <template #roles-cell="{ row }">
        {{ row.original.roles?.join(', ') || '-' }}
      </template>
      <template #actions-cell="{ row }">
        <UButton size="sm" color="neutral" variant="soft" @click="openEditBinding(row.original)">Edit</UButton>
      </template>
    </UTable>

    <UModal v-model:open="showBindingModal" title="Edit Binding">
      <template #body>
        <div class="flex flex-col gap-4">
          <UFormField label="User ID">
            <UInput v-model="bindingForm.user_id" disabled class="w-full" />
          </UFormField>
          <UFormField label="Roles">
            <USelectMenu v-model="bindingForm.roles" :items="roleOptions" value-key="value" multiple class="w-full" />
          </UFormField>
          <UFormField label="Channels">
            <UInput v-model="bindingForm.channelsStr" placeholder="* or comma-separated" class="w-full" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2 w-full">
          <UButton color="neutral" variant="ghost" @click="showBindingModal = false">Cancel</UButton>
          <UButton color="primary" @click="handleBindingSave">Save</UButton>
        </div>
      </template>
    </UModal>

    <USeparator />

    <div class="flex items-center justify-between">
      <h3 class="text-lg font-semibold m-0">Role Mappings</h3>
      <UButton color="primary" size="sm" @click="openCreateMapping">New Mapping</UButton>
    </div>

    <UTable :data="mappings" :columns="mappingColumns" :loading="mappingsLoading">
      <template #actions-cell="{ row }">
        <UButton size="xs" color="error" variant="soft" @click="confirmDeleteMapping(row.original.id)">Delete</UButton>
      </template>
    </UTable>

    <UModal v-model:open="showMappingModal" title="New Role Mapping">
      <template #body>
        <div class="flex flex-col gap-4">
          <UFormField label="Type">
            <USelect v-model="mappingForm.type" :items="mappingTypeOptions" class="w-full" />
          </UFormField>
          <UFormField label="Subject">
            <UInput v-model="mappingForm.subject" placeholder="user_id or group_name" class="w-full" />
          </UFormField>
          <UFormField label="Role">
            <USelect v-model="mappingForm.role" :items="roleOptions" class="w-full" />
          </UFormField>
          <UFormField label="Channel Scope">
            <UInput v-model="mappingForm.channel_scope" placeholder="* or channel ID" class="w-full" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2 w-full">
          <UButton color="neutral" variant="ghost" @click="showMappingModal = false">Cancel</UButton>
          <UButton color="primary" @click="handleMappingSave">Save</UButton>
        </div>
      </template>
    </UModal>

    <UModal v-model:open="showDeleteMappingModal" title="Delete Mapping">
      <template #body>
        <p>Delete this role mapping?</p>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2 w-full">
          <UButton color="neutral" variant="ghost" @click="showDeleteMappingModal = false">Cancel</UButton>
          <UButton color="error" @click="handleDeleteMapping">Delete</UButton>
        </div>
      </template>
    </UModal>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, reactive, computed } from 'vue'
import type { TableColumn } from '@nuxt/ui'
import { api, type Binding, type RoleMapping } from '~/utils/api'

definePageMeta({ middleware: 'admin' })

const toast = useToast()

const bindings = ref<Binding[]>([])
const loading = ref(true)
const showBindingModal = ref(false)
const editingUserId = ref<string | null>(null)
const editingBindingIsAdmin = ref(false)

const mappings = ref<RoleMapping[]>([])
const mappingsLoading = ref(true)
const showMappingModal = ref(false)
const showDeleteMappingModal = ref(false)
const deleteMappingId = ref('')

const roles = ref<{ name: string }[]>([])

const roleOptions = computed(() => roles.value.map(r => ({
  label: r.name,
  value: r.name,
  disabled: editingBindingIsAdmin.value && editingUserId.value !== null && r.name === 'admin',
})))

interface BindingForm {
  user_id: string
  roles: string[]
  channelsStr: string
}

const bindingForm = reactive<BindingForm>({
  user_id: '',
  roles: [],
  channelsStr: '',
})

interface MappingForm {
  type: string
  subject: string
  role: string
  channel_scope: string
}

const mappingForm = reactive<MappingForm>({
  type: 'user',
  subject: '',
  role: '',
  channel_scope: '*',
})

const mappingTypeOptions = [
  { label: 'User', value: 'user' },
  { label: 'Group', value: 'group' },
]

const bindingColumns: TableColumn<Binding>[] = [
  { accessorKey: 'user_id', header: 'User ID' },
  { id: 'roles', header: 'Roles' },
  { id: 'actions', header: 'Actions' },
]

const mappingColumns: TableColumn<RoleMapping>[] = [
  { accessorKey: 'type', header: 'Type' },
  { accessorKey: 'subject', header: 'Subject' },
  { accessorKey: 'role', header: 'Role' },
  { accessorKey: 'channel_scope', header: 'Channel Scope' },
  { id: 'actions', header: 'Actions' },
]

onMounted(async () => {
  await Promise.all([fetchBindings(), fetchRoles(), fetchMappings()])
})

async function fetchBindings() {
  loading.value = true
  try {
    bindings.value = await api.listBindings()
  } catch (e: any) {
    toast.add({ title: e.message, color: 'error' })
  } finally {
    loading.value = false
  }
}

async function fetchRoles() {
  try {
    roles.value = await api.listRoles()
  } catch {
    // ignore
  }
}

async function fetchMappings() {
  mappingsLoading.value = true
  try {
    mappings.value = await api.listRoleMappings()
  } catch (e: any) {
    toast.add({ title: e.message, color: 'error' })
  } finally {
    mappingsLoading.value = false
  }
}

function openEditBinding(binding: Binding) {
  editingUserId.value = binding.user_id
  editingBindingIsAdmin.value = (binding.roles || []).includes('admin')
  bindingForm.user_id = binding.user_id
  bindingForm.roles = binding.roles || []
  bindingForm.channelsStr = (binding.channels || []).join(', ')
  showBindingModal.value = true
}

async function handleBindingSave() {
  const channels = bindingForm.channelsStr.split(',').map(s => s.trim()).filter(Boolean)
  try {
    await api.updateBinding(editingUserId.value!, { roles: bindingForm.roles, channels })
    toast.add({ title: 'Saved', color: 'success' })
    showBindingModal.value = false
    await fetchBindings()
  } catch (e: any) {
    toast.add({ title: e.message, color: 'error' })
  }
}

function openCreateMapping() {
  mappingForm.type = 'user'
  mappingForm.subject = ''
  mappingForm.role = ''
  mappingForm.channel_scope = '*'
  showMappingModal.value = true
}

async function handleMappingSave() {
  if (!mappingForm.subject || !mappingForm.role) {
    toast.add({ title: 'Subject and role are required', color: 'error' })
    return
  }
  try {
    await api.createRoleMapping({
      type: mappingForm.type,
      subject: mappingForm.subject,
      role: mappingForm.role,
      channel_scope: mappingForm.channel_scope || '*',
    })
    toast.add({ title: 'Mapping created', color: 'success' })
    showMappingModal.value = false
    await fetchMappings()
  } catch (e: any) {
    toast.add({ title: e.message, color: 'error' })
  }
}

function confirmDeleteMapping(id: string) {
  deleteMappingId.value = id
  showDeleteMappingModal.value = true
}

async function handleDeleteMapping() {
  try {
    await api.deleteRoleMapping(deleteMappingId.value)
    toast.add({ title: 'Mapping deleted', color: 'success' })
    showDeleteMappingModal.value = false
    await fetchMappings()
  } catch (e: any) {
    toast.add({ title: e.message, color: 'error' })
  }
}
</script>
