<template>
  <div class="flex flex-col gap-4">
    <div class="flex items-center justify-between">
      <h3 class="text-lg font-semibold m-0">Users</h3>
      <UButton color="primary" @click="openCreate">New User</UButton>
    </div>

    <UTable :data="users" :columns="columns" :loading="loading">
      <template #roles-cell="{ row }">
        {{ row.original.roles?.join(', ') || '-' }}
      </template>
      <template #actions-cell="{ row }">
        <div class="flex items-center gap-2">
          <UButton size="sm" color="neutral" variant="soft" @click="openEdit(row.original)">Edit</UButton>
          <UButton size="sm" color="error" variant="ghost" @click="confirmDelete(row.original.id)">Delete</UButton>
        </div>
      </template>
    </UTable>

    <UModal v-model:open="showModal" :title="editingId ? 'Edit User' : 'New User'">
      <template #body>
        <div class="flex flex-col gap-4">
          <UFormField label="Username">
            <UInput v-model="form.username" :disabled="!!editingId" class="w-full" />
          </UFormField>
          <UFormField v-if="!editingId || form.password" label="Password">
            <UInput v-model="form.password" type="password" class="w-full" />
          </UFormField>
          <UFormField label="Roles">
            <USelectMenu v-model="form.roles" :items="roleOptions" value-key="value" multiple class="w-full" />
          </UFormField>
          <UFormField label="Channels">
            <UInput v-model="form.channelsStr" placeholder="* or comma-separated" class="w-full" />
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

    <UModal v-model:open="showDeleteModal" title="Delete User">
      <template #body>
        <p>Delete user {{ deleteTarget }}?</p>
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
import { ref, onMounted, reactive, computed } from 'vue'
import type { TableColumn } from '@nuxt/ui'
import { api, type User } from '~/utils/api'

definePageMeta({ middleware: 'admin' })

const toast = useToast()

const users = ref<User[]>([])
const loading = ref(true)
const showModal = ref(false)
const editingId = ref<string | null>(null)
const editingUserIsAdmin = ref(false)
const roles = ref<{ name: string }[]>([])

const showDeleteModal = ref(false)
const deleteTarget = ref('')

const roleOptions = computed(() => roles.value.map(r => ({
  label: r.name,
  value: r.name,
  disabled: editingUserIsAdmin.value && editingId.value !== null && r.name === 'admin',
})))

const form = reactive({
  username: '',
  password: '',
  roles: [] as string[],
  channelsStr: '',
})

const columns: TableColumn<User>[] = [
  { accessorKey: 'id', header: 'ID' },
  { accessorKey: 'username', header: 'Username' },
  { id: 'roles', header: 'Roles' },
  { id: 'actions', header: 'Actions' },
]

onMounted(async () => {
  await Promise.all([fetchUsers(), fetchRoles()])
})

async function fetchUsers() {
  loading.value = true
  try {
    users.value = await api.listUsers()
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

function openCreate() {
  editingId.value = null
  form.username = ''
  form.password = ''
  form.roles = []
  form.channelsStr = ''
  showModal.value = true
}

function openEdit(user: User) {
  editingId.value = user.id
  editingUserIsAdmin.value = (user.roles || []).includes('admin')
  form.username = user.username
  form.password = ''
  form.roles = user.roles || []
  form.channelsStr = (user.channels || []).join(', ')
  showModal.value = true
}

async function handleSave() {
  const channels = form.channelsStr.split(',').map(s => s.trim()).filter(Boolean)
  try {
    if (editingId.value) {
      const payload: any = { username: form.username, roles: form.roles, channels }
      if (form.password) payload.password = form.password
      await api.updateUser(editingId.value, payload)
    } else {
      await api.createUser({ username: form.username, password: form.password, roles: form.roles, channels })
    }
    toast.add({ title: 'Saved', color: 'success' })
    showModal.value = false
    await fetchUsers()
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
    await api.deleteUser(deleteTarget.value)
    toast.add({ title: 'Deleted', color: 'success' })
    showDeleteModal.value = false
    await fetchUsers()
  } catch (e: any) {
    toast.add({ title: e.message, color: 'error' })
  }
}
</script>
