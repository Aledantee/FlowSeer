<script setup lang="ts">
import { Primitive } from 'reka-ui'

export interface UiBreadcrumbLinkProps {
  asChild?: boolean
  as?: string
  href?: string
}

withDefaults(defineProps<UiBreadcrumbLinkProps>(), {
  asChild: false,
  as: 'a',
  href: undefined,
})
</script>

<template>
  <Primitive
    :as="as"
    :as-child="asChild"
    :href="href"
    class="transition-colors hover:text-foreground cursor-pointer"
  >
    <slot />
  </Primitive>
</template>
