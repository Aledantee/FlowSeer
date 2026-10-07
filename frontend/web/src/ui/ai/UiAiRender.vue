<script lang="ts">
import type { Component } from 'vue'
import UiBadge from '../badge/UiBadge.vue'
import UiStatusBadge from '../badge/UiStatusBadge.vue'
import UiButton from '../button/UiButton.vue'
import UiCard from '../card/UiCard.vue'
import UiMetricCard from '../card/UiMetricCard.vue'
import UiEmptyState from '../empty-state/UiEmptyState.vue'
import UiMeter from '../meter/UiMeter.vue'
import UiProgress from '../progress/UiProgress.vue'
import UiSeparator from '../separator/UiSeparator.vue'
import UiAiEntityChip from './UiAiEntityChip.vue'

export const AI_UI_COMPONENTS: Record<string, Component> = {
  UiCard,
  UiBadge,
  UiStatusBadge,
  UiMetricCard,
  UiMeter,
  UiProgress,
  UiSeparator,
  UiEmptyState,
  UiAiEntityChip,
  UiButton,
}
</script>

<script setup lang="ts">
import { computed, h, inject, type VNodeChild } from 'vue'
import {
  type AiUiNavigateIntent,
  type AiUiNode,
  validateAiUiTree,
} from '../../ai'
import { pageContext, scopeOf, type PageTarget } from '../../navigation/page'
import { useI18n } from 'vue-i18n'

export interface UiAiRenderProps {
  tree: unknown
  errorLabel?: string
}

const props = defineProps<UiAiRenderProps>()

const { t } = useI18n({ useScope: 'global' })
const page = inject(pageContext, null)
const errorText = computed(() => props.errorLabel ?? t('ui.aiRender.error'))
const validatedTree = computed<AiUiNode[] | null>(() => {
  try {
    return validateAiUiTree(props.tree)
  } catch {
    return null
  }
})

function navigate(intent: AiUiNavigateIntent): void {
  if (!page) return
  const target: PageTarget = {
    query: { ...scopeOf(page.location.value), ...intent.target.query },
  }
  if (intent.target.path !== undefined) target.path = intent.target.path
  void page.go(target)
}

function renderNode(node: AiUiNode, key?: number): VNodeChild {
  const component = AI_UI_COMPONENTS[node.component]
  const boundProps: Record<string, unknown> = { ...node.props }
  const text = boundProps.text
  delete boundProps.text

  if (node.component === 'UiButton') {
    const intent = boundProps.intent as AiUiNavigateIntent
    delete boundProps.intent
    boundProps.disabled = !page
    if (page) boundProps.onClick = () => navigate(intent)
  }

  const slots: Record<string, () => VNodeChild> = {}
  if (typeof text === 'string') slots.default = () => text
  if (node.component === 'UiCard' && node.children) {
    slots.default = () =>
      h(
        'div',
        { class: 'flex flex-wrap gap-2' },
        node.children?.map((child, index) => renderNode(child, index)),
      )
  }

  return h(
    component,
    key === undefined ? boundProps : { ...boundProps, key },
    slots,
  )
}

const renderRoot = () => {
  const children =
    validatedTree.value === null
      ? [
          h(
            'div',
            {
              role: 'alert',
              class: 'text-xs text-danger-foreground font-medium',
            },
            errorText.value,
          ),
        ]
      : validatedTree.value.map((node, index) => renderNode(node, index))

  return h(
    'div',
    { class: 'flex flex-col gap-3', 'data-ai-render': '' },
    children,
  )
}
</script>

<template>
  <component :is="renderRoot" />
</template>
