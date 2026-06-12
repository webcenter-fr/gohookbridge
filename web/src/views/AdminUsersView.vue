<template>
  <n-space vertical>
    <n-space justify="space-between" align="center">
      <n-h3 style="margin: 0;">Users</n-h3>
      <n-button type="primary" @click="openCreate">New User</n-button>
    </n-space>

    <n-data-table
      :columns="columns"
      :data="users"
      :loading="loading"
      :bordered="false"
    />

    <n-modal v-model:show="showModal" :title="editingId ? 'Edit User' : 'New User'" preset="card" style="width: 500px;">
      <n-form>
        <n-form-item label="Username">
          <n-input v-model:value="form.username" :disabled="!!editingId" />
        </n-form-item>
        <n-form-item v-if="!editingId || form.password" label="Password">
          <n-input v-model:value="form.password" type="password" />
        </n-form-item>
        <n-form-item label="Roles">
          <n-select v-model:value="form.roles" multiple :options="roleOptions" />
        </n-form-item>
        <n-form-item label="Projects">
          <n-input v-model:value="form.projectsStr" placeholder="* or comma-separated" />
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
import { NSpace, NH3, NButton, NDataTable, NModal, NForm, NFormItem, NInput, NSelect, NTag } from 'naive-ui'
import { api, type User } from '../api/client'
import { useMessage, useDialog } from 'naive-ui'

const message = useMessage()
const dialog = useDialog()

const users = ref<User[]>([])
const loading = ref(true)
const showModal = ref(false)
const editingId = ref<string | null>(null)
const roles = ref<{ name: string }[]>([])

const roleOptions = computed(() => roles.value.map(r => ({ label: r.name, value: r.name })))

const form = reactive({
  username: '',
  password: '',
  roles: [] as string[],
  projectsStr: '',
})

const columns = [
  { title: 'ID', key: 'id' as const },
  { title: 'Username', key: 'username' as const },
  {
    title: 'Roles',
    key: 'roles' as const,
    render: (row: User) => row.roles?.join(', ') || '-',
  },
  {
    title: 'Actions',
    key: 'id' as const,
    render: (row: User) =>
      h(NSpace, {}, () => [
        h(NButton, { size: 'small', onClick: () => openEdit(row) }, () => 'Edit'),
        h(NButton, { size: 'small', type: 'error', quaternary: true, onClick: () => handleDelete(row.id) }, () => 'Delete'),
      ]),
  },
]

onMounted(async () => {
  await Promise.all([fetchUsers(), fetchRoles()])
})

async function fetchUsers() {
  loading.value = true
  try {
    users.value = await api.listUsers()
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

function openCreate() {
  editingId.value = null
  form.username = ''
  form.password = ''
  form.roles = []
  form.projectsStr = ''
  showModal.value = true
}

function openEdit(user: User) {
  editingId.value = user.id
  form.username = user.username
  form.password = ''
  form.roles = user.roles || []
  form.projectsStr = (user.projects || []).join(', ')
  showModal.value = true
}

async function handleSave() {
  const projects = form.projectsStr.split(',').map(s => s.trim()).filter(Boolean)
  try {
    if (editingId.value) {
      const payload: any = { username: form.username, roles: form.roles, projects }
      if (form.password) payload.password = form.password
      await api.updateUser(editingId.value, payload)
    } else {
      await api.createUser({ username: form.username, password: form.password, roles: form.roles, projects })
    }
    message.success('Saved')
    showModal.value = false
    await fetchUsers()
  } catch (e: any) {
    message.error(e.message)
  }
}

function handleDelete(id: string) {
  dialog.warning({
    title: 'Delete User',
    content: `Delete user ${id}?`,
    positiveText: 'Delete',
    onPositiveClick: async () => {
      try {
        await api.deleteUser(id)
        message.success('Deleted')
        await fetchUsers()
      } catch (e: any) {
        message.error(e.message)
      }
    },
  })
}
</script>