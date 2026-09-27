<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import { UiDropdownMenu, UiDropdownMenuItem } from '../ui'
</script>

<template>
  <div class="account-control">
    <UiDropdownMenu align="end" :side-offset="6">
      <template #trigger>
        <button
          class="account-trigger"
          type="button"
          aria-label="Operator account"
          title="Operator"
        >
          <span class="avatar" aria-hidden="true">OP</span>
        </button>
      </template>
      <UiDropdownMenuItem
        class="account-logout"
        disabled
        title="Logout is unavailable until sign-in is connected"
      >
        <AppIcon name="logout" /> Log out
      </UiDropdownMenuItem>
    </UiDropdownMenu>
  </div>
</template>
