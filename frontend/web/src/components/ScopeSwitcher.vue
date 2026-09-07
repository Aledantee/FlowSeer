<script setup lang="ts">
import { computed, ref, useId } from 'vue'

const props = defineProps<{
  label: string
  selected: string
  options: { value: string; label: string; nested?: boolean }[]
}>()
const emit = defineEmits<{ change: [value: string] }>()
const id = useId()
const trigger = ref<HTMLButtonElement>()
const menu = ref<HTMLDivElement>()
const opened = ref(false)
const name = computed(
  () =>
    props.options.find((option) => option.value === props.selected)?.label ??
    'Unavailable selection',
)
let prefix = ''
let lastKey = 0

function open() {
  if (!trigger.value || !menu.value) return
  const rect = trigger.value.getBoundingClientRect()
  const width = Math.min(300, window.innerWidth - 24)
  menu.value.style.left = `${Math.max(12, Math.min(rect.left, window.innerWidth - width - 12))}px`
  menu.value.style.top = `${rect.bottom + 6}px`
  menu.value.style.width = `${width}px`
  menu.value.showPopover()
  opened.value = true
  const options =
    menu.value.querySelectorAll<HTMLButtonElement>('[role="option"]')
  const index = Math.max(
    0,
    props.options.findIndex((option) => option.value === props.selected),
  )
  options[index]?.focus()
}
function choose(value: string) {
  menu.value?.hidePopover()
  trigger.value?.focus()
  emit('change', value)
}
function keydown(event: KeyboardEvent) {
  const options = Array.from(
    menu.value?.querySelectorAll<HTMLButtonElement>('[role="option"]') || [],
  )
  const current = options.findIndex(
    (option) => option === document.activeElement,
  )
  let next: number
  if (event.key === 'ArrowDown') next = (current + 1) % options.length
  else if (event.key === 'ArrowUp')
    next = (current - 1 + options.length) % options.length
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = options.length - 1
  else if (event.key === 'Tab') {
    menu.value?.hidePopover()
    trigger.value?.focus()
    return
  } else if (
    event.key.length === 1 &&
    !event.ctrlKey &&
    !event.metaKey &&
    event.key !== ' '
  ) {
    prefix = Date.now() - lastKey > 600 ? event.key : prefix + event.key
    lastKey = Date.now()
    next = props.options.findIndex((option) =>
      option.label.toLowerCase().startsWith(prefix.toLowerCase()),
    )
  } else return
  event.preventDefault()
  options[next]?.focus()
}
</script>

<template>
  <div class="scope-switcher">
    <button
      ref="trigger"
      class="scope-trigger"
      :title="name"
      :aria-label="`${label}: ${name}`"
      aria-haspopup="listbox"
      :aria-expanded="opened"
      :aria-controls="id"
      @click="opened ? menu?.hidePopover() : open()"
      @keydown.down.prevent="open()"
      @keydown.up.prevent="open()"
    >
      <slot
        ><span>{{ name }}</span></slot
      >
      <span class="scope-chevron" aria-hidden="true">⌄</span>
    </button>
    <div
      :id="id"
      ref="menu"
      popover="auto"
      class="scope-menu"
      role="listbox"
      :aria-label="label"
      @toggle="opened = menu?.matches(':popover-open') ?? false"
      @keydown="keydown"
    >
      <button
        v-for="option in options"
        :key="option.value"
        type="button"
        role="option"
        :aria-selected="option.value === selected"
        :class="{ 'scope-nested': option.nested }"
        tabindex="-1"
        @click="choose(option.value)"
      >
        <span>{{ option.label }}</span
        ><span class="scope-check" aria-hidden="true">{{
          option.value === selected ? '✓' : ''
        }}</span>
      </button>
    </div>
  </div>
</template>
