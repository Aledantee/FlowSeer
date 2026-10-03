import type { Meta, StoryObj } from '@storybook/vue3-vite'
import { ref } from 'vue'
import UiPagination from './UiPagination.vue'

const meta: Meta<typeof UiPagination> = {
  title: 'Ui/Pagination',
  component: UiPagination,
  argTypes: {
    total: { control: 'number' },
    itemsPerPage: { control: 'number' },
    showEdges: { control: 'boolean' },
    siblingCount: { control: 'number' },
  },
}

export default meta
type Story = StoryObj<typeof UiPagination>

export const Default: Story = {
  render: () => ({
    components: { UiPagination },
    setup() {
      const page = ref(1)
      return { page }
    },
    template: `
      <div>
        <UiPagination :total="100" :items-per-page="10" :page="page" @update:page="page = $event" />
        <p class="text-xs text-muted-foreground mt-2">Current page: {{ page }}</p>
      </div>
    `,
  }),
}

export const WithEdges: Story = {
  render: () => ({
    components: { UiPagination },
    setup() {
      const page = ref(5)
      return { page }
    },
    template: `
      <div>
        <UiPagination
          :total="250"
          :items-per-page="10"
          :page="page"
          show-edges
          @update:page="page = $event"
        />
        <p class="text-xs text-muted-foreground mt-2">Current page: {{ page }}</p>
      </div>
    `,
  }),
}

export const FormattedValues: Story = {
  render: () => ({
    components: { UiPagination },
    setup() {
      const page = ref(1500)
      return { page }
    },
    template: `
      <div>
        <UiPagination
          :total="50000"
          :items-per-page="10"
          :page="page"
          show-edges
          @update:page="page = $event"
        />
        <p class="text-xs text-muted-foreground mt-2">Current page: {{ page }}</p>
      </div>
    `,
  }),
}

export const LongText: Story = {
  render: () => ({
    components: { UiPagination },
    setup() {
      const page = ref(12)
      return { page }
    },
    template: `
      <div>
        <UiPagination
          :total="1000000"
          :items-per-page="10"
          :page="page"
          show-edges
          first-label="Navigate to the first page of telemetry records"
          last-label="Navigate to the last page of telemetry records"
          previous-text="Previous page"
          next-text="Next page"
          @update:page="page = $event"
        />
        <p class="text-xs text-muted-foreground mt-2">Current page: {{ page }}</p>
      </div>
    `,
  }),
}
