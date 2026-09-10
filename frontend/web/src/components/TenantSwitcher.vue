<script setup lang="ts">
import type { Tenant } from '../domain/fleet'
import ScopeSwitcher from './ScopeSwitcher.vue'
import TenantLabel from './TenantLabel.vue'
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
    >
      <TenantLabel
        :name="
          tenants.find((tenant) => tenant.id === selected)?.name ||
          'All tenants'
        "
        :icon-url="tenants.find((tenant) => tenant.id === selected)?.iconUrl"
        :scoped="!!selected"
      />
    </ScopeSwitcher>
    <TenantLabel
      v-else
      :name="tenants[0]?.name || 'No tenants available'"
      :icon-url="tenants[0]?.iconUrl"
      scoped
    />
  </div>
</template>
