import type { Meta, StoryObj } from '@storybook/vue3-vite'
import { aiRegistry } from '../../ai'
import type { AiTarget } from '../../ai'
import UiAiEntityChip from './UiAiEntityChip.vue'

const meta: Meta<typeof UiAiEntityChip> = {
  title: 'Ui/Ai/UiAiEntityChip',
  component: UiAiEntityChip,
  argTypes: {
    size: {
      control: 'select',
      options: ['sm', 'md'],
    },
    removable: {
      control: 'boolean',
    },
  },
}

export default meta
type Story = StoryObj<typeof UiAiEntityChip>

export const Device: Story = {
  args: {
    entity: {
      kind: 'device',
      id: 'd1',
      label: 'core-sw-1',
    },
    size: 'md',
    removable: false,
  },
}

export const Site: Story = {
  args: {
    entity: {
      kind: 'site',
      id: 'berlin',
      label: 'Berlin Mitte',
    },
    size: 'md',
    removable: false,
  },
}

export const PlainText: Story = {
  args: {
    entity: {
      kind: 'chart',
      id: 'traffic-1',
      label: 'Aggregate traffic',
    },
    size: 'md',
    removable: false,
  },
}

export const Removable: Story = {
  args: {
    entity: {
      kind: 'device',
      id: 'edge-sw-2',
      label: 'edge-sw-2',
    },
    size: 'md',
    removable: true,
  },
}

const selectedTarget: AiTarget = {
  id: 'standalone:story:ai-entity-chip:selected',
  kind: 'chip',
  label: 'Aggregate traffic',
  context: { state: 'selected' },
}

// The registry draws the selection outline on the chip it registered.
export const Selected: Story = {
  args: {
    ...PlainText.args,
    ai: selectedTarget,
  },
  play: () => {
    aiRegistry.highlight(selectedTarget.id)
  },
}
