<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { UiDialog } from '../ui'
import { BRAND } from '../brand'

const { t } = useI18n({ useScope: 'global' })
const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ closeAutoFocus: [event: Event] }>()
</script>

<template>
  <UiDialog
    v-model:open="open"
    :title="t('view.help.title')"
    @close-auto-focus="emit('closeAutoFocus', $event)"
  >
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
