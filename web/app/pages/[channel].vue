<template>
  <div />
</template>

<script setup lang="ts">
const route = useRoute()
await navigateTo('/channels/' + route.params.channel, { replace: true })
</script>
