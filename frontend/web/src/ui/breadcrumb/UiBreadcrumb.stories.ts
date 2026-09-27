import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiBreadcrumb from './UiBreadcrumb.vue'
import UiBreadcrumbList from './UiBreadcrumbList.vue'
import UiBreadcrumbItem from './UiBreadcrumbItem.vue'
import UiBreadcrumbLink from './UiBreadcrumbLink.vue'
import UiBreadcrumbPage from './UiBreadcrumbPage.vue'
import UiBreadcrumbSeparator from './UiBreadcrumbSeparator.vue'
import UiBreadcrumbEllipsis from './UiBreadcrumbEllipsis.vue'

const sampleItems = [
  { label: 'Fleet', href: '#fleet' },
  { label: 'Europe West', href: '#europe' },
  { label: 'Berlin Core', href: '#berlin' },
  { label: 'Gateway 01', current: true },
]

const meta: Meta<typeof UiBreadcrumb> = {
  title: 'Ui/Breadcrumb',
  component: UiBreadcrumb,
  argTypes: {
    collapsed: { control: 'boolean' },
  },
}

export default meta
type Story = StoryObj<typeof UiBreadcrumb>

export const Default: Story = {
  args: {
    items: sampleItems,
    collapsed: false,
  },
}

export const Collapsed: Story = {
  args: {
    items: sampleItems,
    collapsed: true,
  },
}

export const Composed: Story = {
  render: () => ({
    components: {
      UiBreadcrumb,
      UiBreadcrumbList,
      UiBreadcrumbItem,
      UiBreadcrumbLink,
      UiBreadcrumbPage,
      UiBreadcrumbSeparator,
      UiBreadcrumbEllipsis,
    },
    template: `
      <UiBreadcrumb>
        <UiBreadcrumbList>
          <UiBreadcrumbItem>
            <UiBreadcrumbLink href="#home">Home</UiBreadcrumbLink>
          </UiBreadcrumbItem>
          <UiBreadcrumbSeparator />
          <UiBreadcrumbItem>
            <UiBreadcrumbEllipsis :items="[{ label: 'Network', href: '#net' }, { label: 'Routers', href: '#routers' }]" />
          </UiBreadcrumbItem>
          <UiBreadcrumbSeparator />
          <UiBreadcrumbItem>
            <UiBreadcrumbPage>gw-ber-01</UiBreadcrumbPage>
          </UiBreadcrumbItem>
        </UiBreadcrumbList>
      </UiBreadcrumb>
    `,
  }),
}
