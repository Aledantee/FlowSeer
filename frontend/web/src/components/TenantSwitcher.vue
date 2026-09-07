<script setup lang="ts">
import type { Tenant } from '../domain/fleet'
import ScopeSwitcher from './ScopeSwitcher.vue'
defineProps<{ tenants: Tenant[]; selected: string }>()
const emit = defineEmits<{ change: [tenantId: string] }>()
</script>

<template>
  <div class="tenant-switcher">
    <ScopeSwitcher
      v-if="tenants.length > 1"
      label="Tenant scope"
      :selected="selected"
      :options="[
        { value: '', label: 'All tenants' },
        ...tenants.map((tenant) => ({
          value: tenant.id,
          label: tenant.name,
          nested: !!tenant.parentId,
        })),
      ]"
      @change="emit('change', $event)"
    />
    <span v-else class="tenant-name">{{
      tenants[0]?.name || 'No tenants available'
    }}</span>
  </div>
</template>
