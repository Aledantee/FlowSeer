<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { UiScrollArea, UiTooltip } from '../ui'
import { dockTabShortcut } from './shortcuts'
import AppIcon from '../components/AppIcon.vue'
import type { Health } from '../domain/fleet'
import type { DockTab } from './dock'
import { useFormat } from '../i18n/format'
import { useLabels } from '../i18n/labels'
defineProps<{
  tabs: DockTab[]
  title: (tab: DockTab) => {
    label: string
    detail: string
    icon: string
    health?: Health
    attention?: number
    labelName?: boolean
    detailName?: boolean
    pair?: {
      first: { label: string; name?: boolean }
      second: { label: string; name?: boolean }
    }
  }
  canSplit: boolean
}>()
const { t, n } = useI18n({ useScope: 'global' })
const format = useFormat()
const labels = useLabels()
const emit = defineEmits<{
  open: [id: string]
  split: [id: string]
  close: [id: string]
}>()
function badge(health: Health | undefined, attention: number | undefined) {
  if (health && health !== 'Healthy') return labels.health(health)
  if (attention) return format.counted('view.common.needAttention', attention)
  return ''
}
function formatAttention(attention: number | undefined): string {
  return attention ? n(attention, 'integer') : ''
}
</script>

<template>
  <nav
    v-if="tabs.length"
    class="page-dock absolute z-[7] inset-x-4 bottom-3 flex justify-center pointer-events-none"
    :aria-label="t('view.dock.minimizedPages')"
  >
    <UiScrollArea
      axis="x"
      class="dock-scroll max-w-full pointer-events-auto border border-chrome-border rounded-panel bg-chrome/88 backdrop-blur-xl shadow-lg"
    >
      <TransitionGroup
        name="dock"
        tag="ul"
        class="flex gap-1.5 m-0 p-1.5 list-none"
      >
        <li
          v-for="(tab, index) in tabs"
          :key="tab.id"
          class="dock-tab flex shrink-0 items-center border border-transparent rounded-control text-chrome-foreground hover:bg-chrome-hover"
          :class="{ pair: tab.beside }"
        >
          <UiTooltip
            :label="t('view.common.openNamed', { name: title(tab).label })"
            :hint="
              format.facts([
                title(tab).detail,
                badge(title(tab).health, title(tab).attention),
                canSplit && !tab.beside && t('view.dock.shiftHint'),
              ])
            "
            :shortcut="index < 9 ? dockTabShortcut(index + 1) : undefined"
            :identifier="
              Boolean(
                title(tab).labelName ||
                title(tab).detailName ||
                title(tab).pair?.first.name ||
                title(tab).pair?.second.name,
              )
            "
            side="top"
          >
            <template #label>
              <I18nT
                v-if="title(tab).pair"
                scope="global"
                keypath="view.common.openNamed"
              >
                <template #name>
                  <I18nT scope="global" keypath="view.common.pair">
                    <template #first>
                      <span
                        :translate="
                          title(tab).pair?.first.name ? 'no' : undefined
                        "
                        >{{ title(tab).pair?.first.label }}</span
                      >
                    </template>
                    <template #second>
                      <span
                        :translate="
                          title(tab).pair?.second.name ? 'no' : undefined
                        "
                        >{{ title(tab).pair?.second.label }}</span
                      >
                    </template>
                  </I18nT>
                </template>
              </I18nT>
              <I18nT v-else scope="global" keypath="view.common.openNamed">
                <template #name>
                  <span :translate="title(tab).labelName ? 'no' : undefined">{{
                    title(tab).label
                  }}</span>
                </template>
              </I18nT>
            </template>
            <template #hint>
              <span
                v-if="
                  title(tab).detail ||
                  badge(title(tab).health, title(tab).attention) ||
                  (canSplit && !tab.beside)
                "
              >
                <span
                  v-if="title(tab).detail"
                  :translate="title(tab).detailName ? 'no' : undefined"
                  >{{ title(tab).detail }}</span
                >
                <template v-if="badge(title(tab).health, title(tab).attention)">
                  {{ title(tab).detail ? t('view.common.factSeparator') : ''
                  }}{{ badge(title(tab).health, title(tab).attention) }}
                </template>
                <template v-if="canSplit && !tab.beside">
                  {{
                    title(tab).detail ||
                    badge(title(tab).health, title(tab).attention)
                      ? t('view.common.factSeparator')
                      : ''
                  }}{{ t('view.dock.shiftHint') }}
                </template>
              </span>
            </template>
            <button
              class="dock-open flex items-center gap-2 max-w-[220px] py-1.5 pl-2.5 pr-1.5 border-0 bg-transparent text-inherit text-left cursor-pointer"
              @click="
                $event.shiftKey && canSplit && !tab.beside
                  ? emit('split', tab.id)
                  : emit('open', tab.id)
              "
            >
              <span
                class="dock-icon relative grid shrink-0 [&>svg]:w-3.5 [&>svg]:text-chrome-muted-foreground"
              >
                <AppIcon :name="title(tab).icon" />
                <i
                  v-if="badge(title(tab).health, title(tab).attention)"
                  class="dock-badge absolute -top-1 -right-1.5 min-w-[13px] h-[13px] px-0.5 rounded-full not-italic font-bold text-2xs leading-[13px] text-center text-chrome"
                  :class="[
                    (title(tab).health ?? 'Degraded').toLowerCase() ===
                    'offline'
                      ? 'bg-danger-foreground'
                      : 'bg-warning-foreground',
                    { 'min-w-2 h-2 -top-0.5 -right-1': !title(tab).attention },
                  ]"
                  :aria-label="badge(title(tab).health, title(tab).attention)"
                  >{{ formatAttention(title(tab).attention) }}</i
                >
              </span>
              <span class="min-w-0">
                <strong
                  class="block truncate text-xs font-semibold"
                  :class="{ 'max-w-[180px]': tab.beside }"
                >
                  <I18nT
                    v-if="title(tab).pair"
                    scope="global"
                    tag="span"
                    keypath="view.common.pair"
                  >
                    <template #first>
                      <span
                        :translate="
                          title(tab).pair?.first.name ? 'no' : undefined
                        "
                        >{{ title(tab).pair?.first.label }}</span
                      >
                    </template>
                    <template #second>
                      <span
                        :translate="
                          title(tab).pair?.second.name ? 'no' : undefined
                        "
                        >{{ title(tab).pair?.second.label }}</span
                      >
                    </template>
                  </I18nT>
                  <span
                    v-else
                    :translate="title(tab).labelName ? 'no' : undefined"
                    >{{ title(tab).label }}</span
                  >
                </strong>
                <small
                  class="block truncate text-2xs text-chrome-muted-foreground max-[800px]:hidden"
                  :translate="title(tab).detailName ? 'no' : undefined"
                  >{{ title(tab).detail }}</small
                >
              </span>
            </button>
          </UiTooltip>
          <UiTooltip
            v-if="canSplit && !tab.beside"
            :label="t('view.common.openBeside')"
            :shortcut="index < 9 ? dockTabShortcut(index + 1, true) : undefined"
            side="top"
          >
            <button
              class="dock-action grid place-items-center w-6.5 h-6.5 p-0 border-0 rounded bg-transparent text-chrome-muted-foreground hover:bg-chrome-surface/80 hover:text-chrome-foreground cursor-pointer [&>svg]:w-3.5"
              :aria-label="
                t('view.common.openBesideLabel', { label: title(tab).label })
              "
              @click="emit('split', tab.id)"
            >
              <AppIcon name="panel-right" />
            </button>
          </UiTooltip>
          <UiTooltip :label="t('view.dock.remove')" side="top">
            <button
              class="dock-action grid place-items-center w-6.5 h-6.5 mr-1 p-0 border-0 rounded bg-transparent text-chrome-muted-foreground hover:bg-chrome-surface/80 hover:text-chrome-foreground cursor-pointer [&>svg]:w-3.5"
              :aria-label="t('view.dock.close', { label: title(tab).label })"
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

<style scoped>
.dock-enter-active,
.dock-leave-active,
.dock-move {
  transition:
    opacity 180ms ease,
    transform 180ms ease;
}
.dock-enter-from,
.dock-leave-to {
  opacity: 0;
  transform: translateY(8px) scale(0.96);
}
@media (prefers-reduced-motion: reduce) {
  .dock-enter-active,
  .dock-leave-active,
  .dock-move {
    transition: none;
  }
}
</style>
