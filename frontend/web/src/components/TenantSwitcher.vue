<script setup lang="ts">
import type { Tenant } from '../domain/fleet'
import ScopeSwitcher from './ScopeSwitcher.vue'
defineProps<{ tenants: Tenant[]; selected: string }>()
const emit = defineEmits<{ change: [tenantId: string] }>()
</script>

<template>
  <div
    class="tenant-switcher relative flex items-center gap-2.5 min-w-0 text-chrome-foreground rounded"
  >
    <ScopeSwitcher
      v-if="tenants.length > 1"
      label="Tenant scope"
      placeholder="Search tenants…"
      :selected="selected"
      :options="[
        { value: '', label: 'All tenants' },
        ...tenants.map((tenant) => ({
          value: tenant.id,
          label: tenant.name,
          nested: !!tenant.parentId,
          iconUrl: tenant.iconUrl,
        })),
      ]"
      @change="emit('change', $event)"
    />
    <span
      v-else
      class="tenant-name text-xs font-semibold truncate min-w-0 text-chrome-foreground"
      >{{ tenants[0]?.name || 'No tenants available' }}</span
    >
  </div>
</template>
