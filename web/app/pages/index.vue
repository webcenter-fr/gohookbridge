<template>
  <div class="flex flex-col gap-4">
    <div class="flex items-center justify-between">
      <h3 class="text-lg font-semibold m-0">Dashboard</h3>
      <UButton color="primary" @click="showCreate = true">New Channel</UButton>
    </div>

    <UCard>
      <div class="text-3xl font-bold">{{ channelsStore.channels.length }}</div>
      <div class="text-sm text-(--ui-text-muted)">channels available</div>
    </UCard>

    <h4 class="text-base font-semibold m-0">Channel Shortcuts</h4>
    <div v-if="channelsStore.channels.length > 0" class="grid grid-cols-4 gap-3">
      <UCard
        v-for="ch in channelsStore.channels"
        :key="ch.id"
        :title="ch.id"
        class="cursor-pointer hover:shadow-md"
        @click="navigateTo(`/channels/${ch.id}`)"
      >
        <div class="flex flex-col gap-2">
          <span class="text-xs text-(--ui-text-muted)">{{ ch.id }}</span>
          <UBadge v-if="ch.encryption_mode && ch.encryption_mode !== 'none'" color="warning" size="sm">Encrypted</UBadge>
        </div>
      </UCard>
    </div>
    <div v-else-if="!channelsStore.loading" class="flex flex-col items-center gap-2 py-8 text-(--ui-text-muted)">
      <UIcon name="i-lucide-inbox" class="size-8" />
      <span>No channels available</span>
    </div>

    <UModal v-model:open="showCreate" title="New Channel">
      <template #body>
        <div class="flex flex-col gap-4">
          <UFormField label="Channel ID" :error="errors.id">
            <UInput v-model="formData.id" placeholder="my-webhook-channel" :maxlength="64" class="w-full" />
          </UFormField>
          <UFormField label="Description" :error="errors.description">
            <UTextarea v-model="formData.description" placeholder="Optional" :maxlength="500" class="w-full" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <div class="flex justify-end gap-2 w-full">
          <UButton color="neutral" variant="ghost" @click="showCreate = false">Cancel</UButton>
          <UButton color="primary" :loading="creating" @click="handleCreate">Create</UButton>
        </div>
      </template>
    </UModal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { useChannelsStore } from '~/stores/channels'
import { channelIdError, channelDescriptionError } from '~/utils/validation'

const channelsStore = useChannelsStore()
const toast = useToast()

const showCreate = ref(false)
const creating = ref(false)
const formData = reactive({ id: '', description: '' })
const errors = reactive({ id: '', description: '' })

function validate(): boolean {
  errors.id = channelIdError(formData.id)
  errors.description = channelDescriptionError(formData.description)
  return !errors.id && !errors.description
}

onMounted(() => {
  channelsStore.fetchChannels()
})

async function handleCreate() {
  if (!validate()) return
  creating.value = true
  try {
    await channelsStore.createChannel(formData.id, formData.description || undefined)
    toast.add({ title: 'Channel created', color: 'success' })
    showCreate.value = false
    formData.id = ''
    formData.description = ''
  } catch (e: any) {
    toast.add({ title: e.message, color: 'error' })
  } finally {
    creating.value = false
  }
}
</script>
