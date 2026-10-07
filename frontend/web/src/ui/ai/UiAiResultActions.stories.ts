import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiAiResultActions from './UiAiResultActions.vue'

const meta: Meta<typeof UiAiResultActions> = {
  title: 'Ui/Ai/UiAiResultActions',
  component: UiAiResultActions,
}

export default meta
type Story = StoryObj<typeof UiAiResultActions>

export const Default: Story = {
  args: {
    requestId: 'req-sample-1',
    result: {
      type: 'summary',
      headline: 'Berlin Mitte site is fully operational.',
      tone: 'ok',
      findings: [
        {
          title: 'All 14 switches reporting healthy state.',
          severity: 'info',
          refs: [],
        },
      ],
      metrics: [
        { label: 'Uptime', value: '99.99%' },
        { label: 'Throughput', value: '1.2 Gbps' },
      ],
      next: [],
      sources: [],
    },
  },
}

export const AnswerResult: Story = {
  args: {
    requestId: 'req-sample-2',
    result: {
      type: 'answer',
      text: 'The device core-sw-1 is online with 0 CRC errors across all trunk ports.',
      refs: [],
    },
  },
}
