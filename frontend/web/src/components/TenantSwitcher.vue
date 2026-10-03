<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { Tenant } from '../domain/fleet'
import ScopeSwitcher from './ScopeSwitcher.vue'
defineProps<{ tenants: Tenant[]; selected: string }>()
const emit = defineEmits<{ change: [tenantId: string] }>()
const { t } = useI18n({ useScope: 'global' })
</script>

<template>
  <div
    class="tenant-switcher relative flex items-center gap-2.5 min-w-0 text-chrome-foreground rounded"
  >
    <ScopeSwitcher
      v-if="tenants.length > 1"
      :label="t('view.tenantSwitcher.scopeLabel')"
      :placeholder="t('view.tenantSwitcher.searchPlaceholder')"
      :selected="selected"
      :options="[
        { value: '', label: t('view.common.allTenants') },
        ...tenants.map((tenant) => ({
          value: tenant.id,
          label: tenant.name,
          nested: !!tenant.parentId,
          iconUrl: tenant.iconUrl,
          identifier: true,
        })),
      ]"
      @change="emit('change', $event)"
    />
    <span
      v-else-if="tenants[0]"
      class="tenant-name text-xs font-semibold truncate min-w-0 text-chrome-foreground"
      translate="no"
      >{{ tenants[0].name }}</span
    >
    <span
      v-else
      class="tenant-name text-xs font-semibold truncate min-w-0 text-chrome-foreground"
      >{{ t('view.tenantSwitcher.none') }}</span
    >
  </div>
</template>
