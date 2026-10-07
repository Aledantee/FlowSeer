import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiCard from './UiCard.vue'

const meta: Meta<typeof UiCard> = {
  title: 'Ui/Card',
  component: UiCard,
}

export default meta
type Story = StoryObj<typeof UiCard>

export const Default: Story = {
  render: () => ({
    components: { UiCard },
    setup() {
      const cardTarget = {
        id: 'standalone:story:ui-card-default:card',
        kind: 'card',
        label: 'Card Title',
        context: { updated: 'just now' },
      }
      return { cardTarget }
    },
    template: `
      <UiCard class="max-w-sm" :ai="cardTarget">
        <template #header>
          <h3 class="text-sm font-semibold text-foreground">Card Title</h3>
          <span class="text-xs text-muted-foreground">Action</span>
        </template>
        <p class="text-sm text-muted-foreground">
          This is elevated card content enclosed within a rounded panel surface.
        </p>
        <template #footer>
          <span class="text-xs text-muted-foreground">Updated just now</span>
        </template>
      </UiCard>
    `,
  }),
}
