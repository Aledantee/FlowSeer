<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { UiDialog, UiTooltip } from '../ui'
import AppIcon from './AppIcon.vue'
import { BRAND } from '../brand'

const { t } = useI18n({ useScope: 'global' })
const open = ref(false)
</script>

<template>
  <UiDialog v-model:open="open" :title="t('view.help.title')">
    <template #trigger>
      <UiTooltip :label="t('view.help.trigger')">
        <button
          class="help-button grid place-items-center w-11 h-11 p-0 bg-transparent border-0 text-chrome-foreground rounded hover:bg-chrome-hover cursor-pointer [&>svg]:w-5 [&>svg]:h-5"
          type="button"
          :aria-label="t('view.help.trigger')"
          @click="open = true"
        >
          <AppIcon name="help" />
        </button>
      </UiTooltip>
    </template>
    <div class="space-y-4 text-sm text-foreground">
      <div>
        <h3 class="font-medium text-foreground mb-1">
          {{ t('view.help.scopeHeading') }}
        </h3>
        <p class="text-muted-foreground text-xs leading-relaxed">
          {{ t('view.help.scopeBody') }}
        </p>
      </div>
      <div>
        <h3 class="font-medium text-foreground mb-1">
          {{ t('view.help.findHeading') }}
        </h3>
        <I18nT
          scope="global"
          tag="p"
          class="text-muted-foreground text-xs leading-relaxed"
          keypath="view.help.findBody"
        >
          <template #brand>
            <span translate="no">{{ BRAND }}</span>
          </template>
        </I18nT>
      </div>
      <div>
        <h3 class="font-medium text-foreground mb-1">
          {{ t('view.help.moveHeading') }}
        </h3>
        <p class="text-muted-foreground text-xs leading-relaxed">
          {{ t('view.help.moveBody') }}
        </p>
      </div>
    </div>
  </UiDialog>
</template>
