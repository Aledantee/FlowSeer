import type { Meta, StoryObj } from '@storybook/vue3-vite'
import { computed, h, provide } from 'vue'
import type { AiUiNode } from '../../ai'
import {
  pageContext,
  type PageContext,
  type PageLocation,
  type PageView,
} from '../../navigation/page'
import UiAiRender from './UiAiRender.vue'

const meta: Meta<typeof UiAiRender> = {
  title: 'Ui/Ai/UiAiRender',
  component: UiAiRender,
}

export default meta
type Story = StoryObj<typeof UiAiRender>

const allComponents: AiUiNode[] = [
  { component: 'UiCard', props: {} },
  { component: 'UiBadge', props: { text: 'Primary', variant: 'primary' } },
  { component: 'UiStatusBadge', props: { status: 'Healthy' } },
  { component: 'UiMetricCard', props: { label: 'Devices', value: 24 } },
  { component: 'UiMeter', props: { label: 'CPU', value: 42 } },
  { component: 'UiProgress', props: { modelValue: 68 } },
  { component: 'UiSeparator', props: {} },
  {
    component: 'UiEmptyState',
    props: {
      title: 'No alerts',
      description: 'Everything is operating normally.',
    },
  },
  {
    component: 'UiAiEntityChip',
    props: {
      entity: { kind: 'device', id: 'core-sw-1', label: 'core-sw-1' },
    },
  },
  {
    component: 'UiButton',
    props: {
      text: 'Open device',
      intent: { type: 'navigate', target: { path: '/devices/core-sw-1' } },
    },
  },
]

export const AllComponents: Story = {
  args: { tree: allComponents },
}

export const NestedCard: Story = {
  args: {
    tree: [
      {
        component: 'UiCard',
        props: {},
        children: [
          { component: 'UiStatusBadge', props: { status: 'Degraded' } },
          { component: 'UiBadge', props: { text: 'Two ports down' } },
        ],
      },
    ],
  },
}

function storyPage(): PageContext {
  const location = computed<PageLocation>(() => ({
    path: '/devices',
    query: { tenant: 'acme' },
  }))
  return {
    location,
    view: computed(() => 'devices' as PageView),
    deviceId: computed(() => undefined),
    primary: true,
    query: (key) => location.value.query[key] ?? '',
    go: async () => {},
    href: (target) => target.path ?? location.value.path,
  }
}

export const NavigateButton: Story = {
  render: () => ({
    setup() {
      provide(pageContext, storyPage())
      return () =>
        h(UiAiRender, {
          tree: [
            {
              component: 'UiButton',
              props: {
                text: 'Open device',
                intent: {
                  type: 'navigate',
                  target: { path: '/devices/core-sw-1' },
                },
              },
            },
          ],
        })
    },
  }),
}

export const RejectedTree: Story = {
  args: {
    tree: [
      { component: 'UiBadge', props: { text: 'This node is discarded' } },
      { component: 'div', props: {} },
    ],
  },
}

const longText = 'L'.repeat(500)

export const LongText: Story = {
  args: {
    tree: [
      {
        component: 'UiEmptyState',
        props: {
          title: longText,
          description: longText,
        },
      },
    ],
  },
}
