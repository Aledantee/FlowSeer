<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import { UiDropdownMenu, UiDropdownMenuItem } from '../ui'
</script>

<template>
  <div class="account-control shrink-0 text-chrome-foreground">
    <UiDropdownMenu align="end" :side-offset="6">
      <template #trigger>
        <button
          class="account-trigger grid place-items-center w-11 h-11 border-0 bg-transparent text-inherit p-0 rounded hover:bg-chrome-hover aria-expanded:bg-chrome-hover cursor-pointer"
          type="button"
          aria-label="Operator account"
          title="Operator"
        >
          <span
            class="avatar grid place-items-center w-[26px] h-[26px] rounded-full bg-chrome-surface text-chrome-foreground text-2xs font-semibold"
            aria-hidden="true"
            >OP</span
          >
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
