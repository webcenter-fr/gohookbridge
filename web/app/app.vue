<template>
  <NuxtLoadingIndicator />
  <UApp>
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>
  </UApp>
</template>

<script setup lang="ts">
useHead({
  title: 'gohookbridge',
  link: [{ rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
  meta: [{ name: 'viewport', content: 'width=device-width, initial-scale=1.0' }],
})
</script>
