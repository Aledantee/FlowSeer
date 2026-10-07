<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import AppIcon from './AppIcon.vue'
import { LOGIN_PATH, signOut } from '../session/session'
import { UiDropdownMenu, UiDropdownMenuItem } from '../ui'

const { t } = useI18n({ useScope: 'global' })
const router = useRouter()

async function logOut() {
  signOut()
  await router.push(LOGIN_PATH)
}
</script>

<template>
  <div class="account-control shrink-0 text-chrome-foreground">
    <UiDropdownMenu align="end" :side-offset="6">
      <template #trigger>
        <button
          class="account-trigger grid place-items-center w-11 h-11 border-0 bg-transparent text-inherit p-0 rounded hover:bg-chrome-hover aria-expanded:bg-chrome-hover cursor-pointer"
          type="button"
          :aria-label="t('view.accountMenu.label')"
          :title="t('view.accountMenu.operator')"
        >
          <span
            class="avatar grid place-items-center w-[26px] h-[26px] rounded-full bg-chrome-surface text-chrome-foreground text-2xs font-semibold"
            aria-hidden="true"
            >{{ t('view.accountMenu.initials') }}</span
          >
        </button>
      </template>
      <UiDropdownMenuItem class="account-logout" @select="logOut">
        <AppIcon name="logout" /> {{ t('view.accountMenu.logout') }}
      </UiDropdownMenuItem>
    </UiDropdownMenu>
  </div>
</template>
