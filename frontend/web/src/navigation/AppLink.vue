<script setup lang="ts">
import { computed, inject } from 'vue'
import { pageContext } from './page'
import type { PageContext, PageTarget } from './page'
import { useWorkspace } from './workspace'

// Every link to a FlowSeer page goes through this component, so they all
// behave alike: a click opens the target in the link's own pane, Shift opens
// it in the other pane, Alt sends it to the dock without leaving the page,
// and Cmd, Ctrl, or a middle click leave it to the browser, which opens the
// real href in a new tab.
const props = defineProps<{
  to: PageTarget
  // The pane the link belongs to; defaults to the pane it renders in.
  page?: PageContext
}>()
const injected = inject(pageContext, undefined)
const workspace = useWorkspace()
const owner = computed(() => {
  const page = props.page ?? injected
  if (!page) throw new Error('AppLink must render inside the workspace.')
  return page
})
function navigate(event: MouseEvent) {
  if (
    event.defaultPrevented ||
    event.button !== 0 ||
    event.metaKey ||
    event.ctrlKey
  )
    return
  event.preventDefault()
  void workspace.follow(owner.value, props.to, {
    beside: event.shiftKey,
    dock: event.altKey,
  })
}
</script>

<template>
  <a :href="owner.href(to)" @click="navigate"><slot /></a>
</template>
