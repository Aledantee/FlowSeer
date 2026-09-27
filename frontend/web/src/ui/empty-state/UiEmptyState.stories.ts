import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiEmptyState from './UiEmptyState.vue'
import UiButton from '../button/UiButton.vue'

const meta: Meta<typeof UiEmptyState> = {
  title: 'Ui/EmptyState',
  component: UiEmptyState,
  argTypes: {
    title: { control: 'text' },
    description: { control: 'text' },
  },
}

export default meta
type Story = StoryObj<typeof UiEmptyState>

export const Default: Story = {
  args: {
    title: 'No devices found',
    description: 'There are no active devices matching your filter criteria.',
  },
  render: (args) => ({
    components: { UiEmptyState },
    setup() {
      return { args }
    },
    template: '<UiEmptyState v-bind="args" />',
  }),
}

export const WithIconAndActions: Story = {
  args: {
    title: 'No clients connected',
    description: 'Clients will appear here once connected to the network.',
  },
  render: (args) => ({
    components: { UiEmptyState, UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiEmptyState v-bind="args">
        <template #icon>
          <svg xmlns="http://www.w3.org/2000/svg" class="h-10 w-10 text-muted-foreground" fill="none" viewBox="0 0 24 24" stroke="currentColor" aria-hidden="true">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z" />
          </svg>
        </template>
        <template #actions>
          <UiButton variant="primary" size="sm">Add Client</UiButton>
          <UiButton variant="secondary" size="sm">Refresh</UiButton>
        </template>
      </UiEmptyState>
    `,
  }),
}
