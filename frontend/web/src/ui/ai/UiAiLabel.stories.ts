import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiAiLabel from './UiAiLabel.vue'

const meta: Meta<typeof UiAiLabel> = {
  title: 'Ui/Ai/UiAiLabel',
  component: UiAiLabel,
}

export default meta
type Story = StoryObj<typeof UiAiLabel>

export const Default: Story = {
  args: {
    request: {
      requestId: 'req-sample',
      action: 'summary',
      history: [],
      targets: [
        {
          id: 'dev-1',
          kind: 'device',
          view: 'devices',
          label: 'Core Switch 1',
          context: {
            health: 'Degraded',
            traffic: '840 Mbps',
          },
        },
      ],
    },
    sources: [
      { kind: 'device', id: 'gw-1', label: 'Gateway 1' },
      'telemetry.interface_octets',
    ],
  },
}

export const InitiallyOpen: Story = {
  args: {
    ...Default.args,
    open: true,
  },
}
