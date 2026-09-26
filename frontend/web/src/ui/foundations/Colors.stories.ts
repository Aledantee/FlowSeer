import type { Meta, StoryObj } from '@storybook/vue3-vite'
import ColorTable from './ColorTable.vue'

const meta: Meta<typeof ColorTable> = {
  title: 'Foundations/Colors',
  component: ColorTable,
  parameters: {
    layout: 'fullscreen',
  },
}

export default meta
type Story = StoryObj<typeof ColorTable>

export const Default: Story = {}
