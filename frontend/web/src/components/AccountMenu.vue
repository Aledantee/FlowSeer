<script setup lang="ts">
import { ref, useId } from 'vue'
import AppIcon from './AppIcon.vue'
const id = useId()
const menu = ref<HTMLDivElement>()
const open = ref(false)
</script>

<template>
  <footer class="account-footer">
    <button
      class="account-trigger"
      aria-label="Operator account"
      title="Operator"
      :popovertarget="id"
      :aria-expanded="open"
      :aria-controls="id"
    >
      <span class="avatar" aria-hidden="true">OP</span>
      <span>Operator</span>
      <span aria-hidden="true">⌃</span>
    </button>
    <div
      :id="id"
      ref="menu"
      popover="auto"
      class="account-menu"
      @toggle="open = menu?.matches(':popover-open') ?? false"
    >
      <button
        class="account-logout"
        disabled
        title="Logout is unavailable until sign-in is connected"
      >
        <AppIcon name="logout" /> Log out
      </button>
    </div>
  </footer>
</template>
