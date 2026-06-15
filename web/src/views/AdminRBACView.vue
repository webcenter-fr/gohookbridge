<template>
  <n-space vertical>
    <n-h3>Role Bindings</n-h3>

    <n-data-table
      :columns="columns"
      :data="bindings"
      :loading="loading"
      :bordered="false"
    />

    <n-modal v-model:show="showModal" title="Edit Binding" preset="card" style="width: 500px;">
      <n-form>
        <n-form-item label="User ID">
          <n-input v-model:value="form.user_id" disabled />
        </n-form-item>
        <n-form-item label="Roles">
          <n-select v-model:value="form.roles" multiple :options="roleOptions" />
        </n-form-item>
        <n-form-item label="Channels">
          <n-input v-model:value="form.channelsStr" placeholder="* or comma-separated" />
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
import { ref, h, onMounted, reactive, computed } from 'vue'
import { NSpace, NH3, NButton, NDataTable, NModal, NForm, NFormItem, NInput, NSelect } from 'naive-ui'
import { api, type Binding } from '../api/client'
import { useMessage } from 'naive-ui'

const message = useMessage()

const bindings = ref<Binding[]>([])
const loading = ref(true)
const showModal = ref(false)
const roles = ref<{ name: string }[]>([])
const editingUserId = ref<string | null>(null)

const roleOptions = computed(() => roles.value.map(r => ({ label: r.name, value: r.name })))

const form = reactive({
  user_id: '',
  roles: [] as string[],
  channelsStr: '',
})

const columns = [
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
      h(NButton, { size: 'small', onClick: () => openEdit(row) }, () => 'Edit'),
  },
]

onMounted(async () => {
  await Promise.all([fetchBindings(), fetchRoles()])
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

function openEdit(binding: Binding) {
  editingUserId.value = binding.user_id
  form.user_id = binding.user_id
  form.roles = binding.roles || []
  form.channelsStr = (binding.channels || []).join(', ')
  showModal.value = true
}

async function handleSave() {
  const channels = form.channelsStr.split(',').map(s => s.trim()).filter(Boolean)
  try {
    await api.updateBinding(editingUserId.value!, { roles: form.roles, channels })
    message.success('Saved')
    showModal.value = false
    await fetchBindings()
  } catch (e: any) {
    message.error(e.message)
  }
}
</script>