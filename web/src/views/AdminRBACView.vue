<template>
  <n-space vertical>
    <n-h3>Role Bindings</n-h3>

    <n-data-table
      :columns="bindingColumns"
      :data="bindings"
      :loading="loading"
      :bordered="false"
    />

    <n-modal v-model:show="showBindingModal" title="Edit Binding" preset="card" style="width: 500px;">
      <n-form>
        <n-form-item label="User ID">
          <n-input v-model:value="bindingForm.user_id" disabled />
        </n-form-item>
        <n-form-item label="Roles">
          <n-select v-model:value="bindingForm.roles" multiple :options="roleOptions" />
        </n-form-item>
        <n-form-item label="Channels">
          <n-input v-model:value="bindingForm.channelsStr" placeholder="* or comma-separated" />
        </n-form-item>
        <n-space justify="end">
          <n-button @click="showBindingModal = false">Cancel</n-button>
          <n-button type="primary" @click="handleBindingSave">Save</n-button>
        </n-space>
      </n-form>
    </n-modal>

    <n-divider />

    <n-space justify="space-between" align="center">
      <n-h3 style="margin: 0;">Role Mappings</n-h3>
      <n-button type="primary" size="small" @click="openCreateMapping">New Mapping</n-button>
    </n-space>

    <n-data-table
      :columns="mappingColumns"
      :data="mappings"
      :loading="mappingsLoading"
      :bordered="false"
    />

    <n-modal v-model:show="showMappingModal" title="New Role Mapping" preset="card" style="width: 500px;">
      <n-form>
        <n-form-item label="Type">
          <n-select v-model:value="mappingForm.type" :options="mappingTypeOptions" />
        </n-form-item>
        <n-form-item label="Subject">
          <n-input v-model:value="mappingForm.subject" placeholder="user_id or group_name" />
        </n-form-item>
        <n-form-item label="Role">
          <n-select v-model:value="mappingForm.role" :options="roleOptions" />
        </n-form-item>
        <n-form-item label="Channel Scope">
          <n-input v-model:value="mappingForm.channel_scope" placeholder="* or channel ID" />
        </n-form-item>
        <n-space justify="end">
          <n-button @click="showMappingModal = false">Cancel</n-button>
          <n-button type="primary" @click="handleMappingSave">Save</n-button>
        </n-space>
      </n-form>
    </n-modal>
  </n-space>
</template>

<script setup lang="ts">
import { ref, h, onMounted, reactive, computed } from 'vue'
import { NSpace, NH3, NButton, NDataTable, NModal, NForm, NFormItem, NInput, NSelect, NDivider, useMessage, useDialog } from 'naive-ui'
import { api, type Binding, type RoleMapping } from '../api/client'

const message = useMessage()
const dialog = useDialog()

const bindings = ref<Binding[]>([])
const loading = ref(true)
const showBindingModal = ref(false)
const editingUserId = ref<string | null>(null)
const editingBindingIsAdmin = ref(false)

const mappings = ref<RoleMapping[]>([])
const mappingsLoading = ref(true)
const showMappingModal = ref(false)

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

const bindingColumns = [
  { title: 'User ID', key: 'user_id' as const },
  {
    title: 'Roles',
    key: 'roles' as const,
    render: (row: Binding) => row.roles?.join(', ') || '-',
  },
  {
    title: 'Actions',
    key: 'user_id' as const,
    render: (row: Binding) =>
      h(NButton, { size: 'small', onClick: () => openEditBinding(row) }, () => 'Edit'),
  },
]

const mappingColumns = [
  { title: 'Type', key: 'type' as const },
  { title: 'Subject', key: 'subject' as const },
  { title: 'Role', key: 'role' as const },
  { title: 'Channel Scope', key: 'channel_scope' as const },
  {
    title: 'Actions',
    key: 'id' as const,
    render: (row: RoleMapping) =>
      h(NButton, { size: 'tiny', type: 'error', secondary: true, onClick: () => handleDeleteMapping(row.id) }, () => 'Delete'),
  },
]

onMounted(async () => {
  await Promise.all([fetchBindings(), fetchRoles(), fetchMappings()])
})

async function fetchBindings() {
  loading.value = true
  try {
    bindings.value = await api.listBindings()
  } catch (e: any) {
    message.error(e.message)
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
    message.error(e.message)
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
    message.success('Saved')
    showBindingModal.value = false
    await fetchBindings()
  } catch (e: any) {
    message.error(e.message)
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
    message.error('Subject and role are required')
    return
  }
  try {
    await api.createRoleMapping({
      type: mappingForm.type,
      subject: mappingForm.subject,
      role: mappingForm.role,
      channel_scope: mappingForm.channel_scope || '*',
    })
    message.success('Mapping created')
    showMappingModal.value = false
    await fetchMappings()
  } catch (e: any) {
    message.error(e.message)
  }
}

async function handleDeleteMapping(id: string) {
  dialog.warning({
    title: 'Delete Mapping',
    content: 'Delete this role mapping?',
    positiveText: 'Delete',
    onPositiveClick: async () => {
      try {
        await api.deleteRoleMapping(id)
        message.success('Mapping deleted')
        await fetchMappings()
      } catch (e: any) {
        message.error(e.message)
      }
    },
  })
}
</script>