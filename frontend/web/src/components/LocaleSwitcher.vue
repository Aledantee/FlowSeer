<script setup lang="ts">
import { computed, ref } from 'vue'
import { I18nT, useI18n } from 'vue-i18n'
import { UiTooltip } from '../ui'
import { saveLocale } from '../i18n/locale'
import type { WebLocale } from '../i18n'

const { t, locale } = useI18n({ useScope: 'global' })

// A language's own name, so a reader who cannot read the page finds it.
function nameOf(code: WebLocale) {
  return new Intl.DisplayNames(code, { type: 'language' }).of(code) ?? code
}

const current = computed(() => locale.value as WebLocale)
const other = computed<WebLocale>(() => (current.value === 'en' ? 'de' : 'en'))
// The last press as a state, not as text, so the status follows a locale
// switch.
const changed = ref<'saved' | 'unsaved' | ''>('')

function toggleLocale() {
  const next = other.value
  locale.value = next
  changed.value = saveLocale(next) ? 'saved' : 'unsaved'
}
</script>

<template>
  <UiTooltip :label="t('view.localeSwitcher.label', { language: nameOf(other) })">
    <button
      class="locale-switcher grid place-items-center w-11 h-11 p-0 bg-transparent text-chrome-foreground border-0 rounded hover:bg-chrome-hover cursor-pointer text-xs font-semibold"
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
  <span class="sr-only" role="status">
    <I18nT
      v-if="changed === 'saved'"
      keypath="view.localeSwitcher.changed"
      scope="global"
    >
      <template #language>
        <span :lang="current" translate="no">{{ nameOf(current) }}</span>
      </template>
    </I18nT>
    <template v-else-if="changed === 'unsaved'">{{
      t('view.localeSwitcher.unsaved')
    }}</template>
  </span>
</template>
