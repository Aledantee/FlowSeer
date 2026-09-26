<script setup lang="ts">
import ScrollArea from './ScrollArea.vue'
import { computed, nextTick, ref, useId } from 'vue'
import AppIcon from './AppIcon.vue'

export interface ScopeOption {
  value: string
  label: string
  // Hierarchy depth, drawn as an indent while the list is unfiltered.
  nested?: boolean
  iconUrl?: string
}
const props = defineProps<{
  label: string
  selected: string
  options: ScopeOption[]
  placeholder?: string
}>()
const emit = defineEmits<{ change: [value: string] }>()
const id = useId()
const trigger = ref<HTMLButtonElement>()
const menu = ref<HTMLDivElement>()
const input = ref<HTMLInputElement>()
const opened = ref(false)
const search = ref('')
const active = ref(0)
const name = computed(
  () =>
    props.options.find((option) => option.value === props.selected)?.label ??
    'Unavailable selection',
)
const matches = computed(() => {
  const query = search.value.trim().toLowerCase()
  return props.options.filter((option) =>
    option.label.toLowerCase().includes(query),
  )
})

async function open() {
  if (!trigger.value || !menu.value) return
  const rect = trigger.value.getBoundingClientRect()
  const width = Math.min(280, window.innerWidth - 24)
  menu.value.style.left = `${Math.max(12, Math.min(rect.left, window.innerWidth - width - 12))}px`
  menu.value.style.top = `${rect.bottom + 6}px`
  menu.value.style.width = `${width}px`
  search.value = ''
  active.value = Math.max(
    0,
    props.options.findIndex((option) => option.value === props.selected),
  )
  menu.value.showPopover()
  opened.value = true
  input.value?.focus()
  await nextTick()
  scrollActive()
}
function close() {
  menu.value?.hidePopover()
  trigger.value?.focus()
}
function choose(value: string) {
  close()
  emit('change', value)
}
function scrollActive() {
  menu.value
    ?.querySelector(`#${CSS.escape(`${id}-${active.value}`)}`)
    ?.scrollIntoView({ block: 'nearest' })
}
function filter() {
  active.value = 0
}
function keydown(event: KeyboardEvent) {
  const count = matches.value.length
  if (event.key === 'ArrowDown' && count)
    active.value = (active.value + 1) % count
  else if (event.key === 'ArrowUp' && count)
    active.value = (active.value - 1 + count) % count
  else if (event.key === 'Enter') {
    const option = matches.value[active.value]
    if (option) choose(option.value)
  } else if (event.key === 'Tab') {
    close()
    return
  } else return
  event.preventDefault()
  void nextTick(scrollActive)
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
      @click="opened ? close() : open()"
      @keydown.down.prevent="open()"
      @keydown.up.prevent="open()"
    >
      <slot
        ><span>{{ name }}</span></slot
      >
      <AppIcon class="scope-chevron" name="chevron" />
    </button>
    <div
      :id="id"
      ref="menu"
      popover="auto"
      class="scope-menu"
      @toggle="opened = menu?.matches(':popover-open') ?? false"
    >
      <label class="scope-search"
        ><AppIcon name="search" /><input
          ref="input"
          v-model="search"
          role="combobox"
          aria-autocomplete="list"
          :aria-expanded="opened"
          :aria-controls="`${id}-list`"
          :aria-activedescendant="
            matches.length ? `${id}-${active}` : undefined
          "
          :aria-label="`Search ${label.toLowerCase()}`"
          :placeholder="placeholder ?? 'Search…'"
          @input="filter"
          @keydown="keydown"
      /></label>
      <ScrollArea viewport-class="scope-viewport">
        <div :id="`${id}-list`" role="listbox" :aria-label="label">
          <div
            v-for="(option, index) in matches"
            :id="`${id}-${index}`"
            :key="option.value"
            role="option"
            :aria-selected="option.value === selected"
            :class="{
              'scope-option': true,
              'scope-nested': option.nested && !search,
              'scope-active': index === active,
            }"
            @mousemove="active = index"
            @mousedown.prevent
            @click="choose(option.value)"
          >
            <img
              v-if="option.iconUrl"
              class="scope-avatar"
              :src="option.iconUrl"
              alt=""
            /><span>{{ option.label }}</span
            ><AppIcon
              v-if="option.value === selected"
              class="scope-check"
              name="check"
            />
          </div>
          <p v-if="!matches.length" class="scope-empty">No matches</p>
        </div>
      </ScrollArea>
    </div>
  </div>
</template>
