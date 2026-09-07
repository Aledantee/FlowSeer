<script setup lang="ts">
import { ref, useId } from 'vue'
import AppIcon from './AppIcon.vue'
const id = useId()
const trigger = ref<HTMLButtonElement>()
const menu = ref<HTMLDivElement>()
const open = ref(false)
function positionMenu() {
  if (!trigger.value || !menu.value) return
  const rect = trigger.value.getBoundingClientRect()
  menu.value.style.top = `${rect.bottom + 6}px`
  menu.value.style.right = `${Math.max(12, window.innerWidth - rect.right)}px`
}
</script>

<template>
  <div class="account-control">
    <button
      ref="trigger"
      class="account-trigger"
      type="button"
      aria-label="Operator account"
      title="Operator"
      :popovertarget="id"
      :aria-expanded="open"
      :aria-controls="id"
      @click="positionMenu"
    >
      <span class="avatar" aria-hidden="true">OP</span>
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
  </div>
</template>
