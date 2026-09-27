<script setup lang="ts">
import { UiScrollArea, UiTooltip } from '../ui'
import { dockTabShortcut, keysOf } from './shortcuts'
import AppIcon from '../components/AppIcon.vue'
import type { Health } from '../domain/fleet'
import type { DockTab } from './dock'
defineProps<{
  tabs: DockTab[]
  title: (tab: DockTab) => {
    label: string
    detail: string
    icon: string
    health?: Health
    attention?: number
  }
  canSplit: boolean
}>()
const emit = defineEmits<{
  open: [id: string]
  split: [id: string]
  close: [id: string]
}>()
function badge(health: Health | undefined, attention: number | undefined) {
  if (health && health !== 'Healthy') return health
  if (attention) return `${attention} need attention`
  return ''
}
</script>

<template>
  <nav v-if="tabs.length" class="page-dock" aria-label="Minimized pages">
    <UiScrollArea axis="x" class="dock-scroll">
      <TransitionGroup name="dock" tag="ul">
        <li
          v-for="(tab, index) in tabs"
          :key="tab.id"
          :class="['dock-tab', { pair: tab.beside }]"
        >
          <UiTooltip
            :label="`Open ${title(tab).label}`"
            :hint="
              [
                title(tab).detail,
                badge(title(tab).health, title(tab).attention),
                canSplit && !tab.beside
                  ? 'Shift-click opens it side by side'
                  : '',
                index < 9 ? keysOf(dockTabShortcut(index + 1)).join('+') : '',
              ]
                .filter(Boolean)
                .join(' · ')
            "
            side="top"
          >
            <button
              class="dock-open"
              @click="
                $event.shiftKey && canSplit && !tab.beside
                  ? emit('split', tab.id)
                  : emit('open', tab.id)
              "
            >
              <span class="dock-icon"
                ><AppIcon :name="title(tab).icon" /><i
                  v-if="badge(title(tab).health, title(tab).attention)"
                  :class="[
                    'dock-badge',
                    (title(tab).health ?? 'Degraded').toLowerCase(),
                  ]"
                  :aria-label="badge(title(tab).health, title(tab).attention)"
                  >{{ title(tab).attention ?? '' }}</i
                ></span
              >
              <span
                ><strong>{{ title(tab).label }}</strong
                ><small>{{ title(tab).detail }}</small></span
              >
            </button>
          </UiTooltip>
          <UiTooltip
            v-if="canSplit && !tab.beside"
            label="Open side by side"
            :hint="
              index < 9
                ? keysOf(dockTabShortcut(index + 1, true)).join('+')
                : undefined
            "
            side="top"
          >
            <button
              class="dock-action"
              :aria-label="`Open ${title(tab).label} side by side`"
              @click="emit('split', tab.id)"
            >
              <AppIcon name="panel-right" />
            </button>
          </UiTooltip>
          <UiTooltip label="Remove from dock" side="top">
            <button
              class="dock-action"
              :aria-label="`Close ${title(tab).label}`"
              @click="emit('close', tab.id)"
            >
              <AppIcon name="close" />
            </button>
          </UiTooltip>
        </li>
      </TransitionGroup>
    </UiScrollArea>
  </nav>
</template>
