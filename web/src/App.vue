<template>
  <n-config-provider :theme="darkTheme">
    <n-loading-bar-provider>
      <n-message-provider>
        <n-notification-provider>
          <n-dialog-provider>
            <router-view />
          </n-dialog-provider>
        </n-notification-provider>
      </n-message-provider>
    </n-loading-bar-provider>
  </n-config-provider>
</template>

<script setup lang="ts">
import { darkTheme } from 'naive-ui'
</script>

<style>
body {
  margin: 0;
  background: #0F172A;
  color: #F8FAFC;
  font-family: 'Inter', system-ui, -apple-system, sans-serif;
}
</style>