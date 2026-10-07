<script setup lang="ts">
import { computed, ref, useTemplateRef } from 'vue'
import { I18nT, useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import AppIcon from './AppIcon.vue'
import HelpDialog from './HelpDialog.vue'
import ReportBugDialog from './ReportBugDialog.vue'
import { useTheme } from './useTheme'
import { saveLocale } from '../i18n/locale'
import type { WebLocale } from '../i18n'
import { LOGIN_PATH, signOut } from '../session/session'
import {
  UiDropdownMenu,
  UiDropdownMenuItem,
  UiDropdownMenuSeparator,
  UiTooltip,
} from '../ui'

// Signed out there is no account, so the theme and language controls stand
// in the top bar on their own.
defineProps<{ signedIn?: boolean }>()

const { t, locale } = useI18n({ useScope: 'global' })
const router = useRouter()
const { theme, announcement, toggleTheme } = useTheme()
const trigger = useTemplateRef('trigger')

// A language's own name, so a reader who cannot read the page finds it.
function nameOf(code: WebLocale) {
  return new Intl.DisplayNames(code, { type: 'language' }).of(code) ?? code
}

const current = computed(() => locale.value as WebLocale)
const other = computed<WebLocale>(() => (current.value === 'en' ? 'de' : 'en'))
// The last choice as a state, not as text, so the status follows a locale
// switch.
const localeChanged = ref<'saved' | 'unsaved' | ''>('')

function toggleLocale() {
  const next = other.value
  locale.value = next
  localeChanged.value = saveLocale(next) ? 'saved' : 'unsaved'
}

// A dialog opens once the menu has closed. Opened from the item's select
// handler, the two layers can leave the body without pointer events.
const pending = ref<'help' | 'report' | ''>('')
const helpOpen = ref(false)
const reportOpen = ref(false)

function menuClosed(event: Event) {
  if (!pending.value) return
  event.preventDefault()
  if (pending.value === 'help') helpOpen.value = true
  else reportOpen.value = true
  pending.value = ''
}

// The menu item that opened the dialog is gone when the dialog closes.
function dialogClosed(event: Event) {
  event.preventDefault()
  trigger.value?.focus()
}

async function logOut() {
  signOut()
  await router.push(LOGIN_PATH)
}
</script>

<template>
  <div class="account-control shrink-0 text-chrome-foreground">
    <div v-if="!signedIn" class="flex items-center gap-1.5">
      <UiTooltip
        :label="
          theme === 'dark'
            ? t('view.themeSwitcher.switchToLight')
            : t('view.themeSwitcher.switchToDark')
        "
      >
        <button
          class="account-theme grid place-items-center w-11 h-11 border-0 bg-transparent text-inherit p-0 rounded hover:bg-chrome-hover cursor-pointer [&>svg]:w-5 [&>svg]:h-5"
          type="button"
          role="switch"
          :aria-label="t('view.themeSwitcher.label')"
          :aria-checked="theme === 'dark'"
          @click="toggleTheme"
        >
          <AppIcon :name="theme === 'dark' ? 'moon' : 'sun'" />
        </button>
      </UiTooltip>
      <UiTooltip
        :label="t('view.localeSwitcher.label', { language: nameOf(other) })"
        identifier
      >
        <template #label>
          <I18nT keypath="view.localeSwitcher.label" scope="global">
            <template #language>
              <span :lang="other" translate="no">{{ nameOf(other) }}</span>
            </template>
          </I18nT>
        </template>
        <button
          class="account-locale grid place-items-center w-11 h-11 border-0 bg-transparent text-inherit p-0 rounded hover:bg-chrome-hover cursor-pointer text-xs font-semibold"
          type="button"
          @click="toggleLocale"
        >
          <span translate="no" aria-hidden="true">{{
            current.toUpperCase()
          }}</span>
          <span class="sr-only">
            <I18nT keypath="view.localeSwitcher.label" scope="global">
              <template #language>
                <span :lang="other" translate="no">{{ nameOf(other) }}</span>
              </template>
            </I18nT>
          </span>
        </button>
      </UiTooltip>
    </div>
    <UiDropdownMenu
      v-else
      align="end"
      :side-offset="6"
      @close-auto-focus="menuClosed"
    >
      <template #trigger>
        <button
          ref="trigger"
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
      <UiDropdownMenuItem class="account-help" @select="pending = 'help'">
        <AppIcon name="help" /> {{ t('view.help.trigger') }}
      </UiDropdownMenuItem>
      <UiDropdownMenuItem class="account-report" @select="pending = 'report'">
        <AppIcon name="bug" /> {{ t('view.reportBug.trigger') }}
      </UiDropdownMenuItem>
      <UiDropdownMenuSeparator />
      <UiDropdownMenuItem class="account-theme" @select="toggleTheme">
        <AppIcon :name="theme === 'dark' ? 'sun' : 'moon'" />
        {{
          theme === 'dark'
            ? t('view.themeSwitcher.switchToLight')
            : t('view.themeSwitcher.switchToDark')
        }}
      </UiDropdownMenuItem>
      <UiDropdownMenuItem
        class="account-locale"
        :text-value="
          t('view.localeSwitcher.label', { language: nameOf(other) })
        "
        @select="toggleLocale"
      >
        <span
          class="grid place-items-center w-[18px] text-2xs font-semibold"
          translate="no"
          aria-hidden="true"
          >{{ other.toUpperCase() }}</span
        >
        <span>
          <I18nT keypath="view.localeSwitcher.label" scope="global">
            <template #language>
              <span :lang="other" translate="no">{{ nameOf(other) }}</span>
            </template>
          </I18nT>
        </span>
      </UiDropdownMenuItem>
      <UiDropdownMenuSeparator />
      <UiDropdownMenuItem class="account-logout" @select="logOut">
        <AppIcon name="logout" /> {{ t('view.accountMenu.logout') }}
      </UiDropdownMenuItem>
    </UiDropdownMenu>
    <HelpDialog v-model:open="helpOpen" @close-auto-focus="dialogClosed" />
    <ReportBugDialog
      v-model:open="reportOpen"
      @close-auto-focus="dialogClosed"
    />
    <span class="account-theme-status sr-only" role="status">{{
      announcement
    }}</span>
    <span class="account-locale-status sr-only" role="status">
      <I18nT
        v-if="localeChanged === 'saved'"
        keypath="view.localeSwitcher.changed"
        scope="global"
      >
        <template #language>
          <span :lang="current" translate="no">{{ nameOf(current) }}</span>
        </template>
      </I18nT>
      <template v-else-if="localeChanged === 'unsaved'">{{
        t('view.localeSwitcher.unsaved')
      }}</template>
    </span>
  </div>
</template>
