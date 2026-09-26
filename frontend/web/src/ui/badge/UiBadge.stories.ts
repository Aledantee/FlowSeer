import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiBadge from './UiBadge.vue'

const meta: Meta<typeof UiBadge> = {
  title: 'Ui/Badge',
  component: UiBadge,
  argTypes: {
    variant: {
      control: 'select',
      options: [
        'default',
        'outline',
        'primary',
        'accent',
        'success',
        'warning',
        'danger',
        'info',
      ],
    },
    size: {
      control: 'select',
      options: ['sm', 'md'],
    },
  },
}

export default meta
type Story = StoryObj<typeof UiBadge>

export const Default: Story = {
  args: {
    variant: 'default',
  },
  render: (args) => ({
    components: { UiBadge },
    setup() {
      return { args }
    },
    template: '<UiBadge v-bind="args">Default Badge</UiBadge>',
  }),
}

export const Outline: Story = {
  args: {
    variant: 'outline',
  },
  render: (args) => ({
    components: { UiBadge },
    setup() {
      return { args }
    },
    template: '<UiBadge v-bind="args">Outline Badge</UiBadge>',
  }),
}

export const Primary: Story = {
  args: {
    variant: 'primary',
  },
  render: (args) => ({
    components: { UiBadge },
    setup() {
      return { args }
    },
    template: '<UiBadge v-bind="args">Primary Badge</UiBadge>',
  }),
}

export const Accent: Story = {
  args: {
    variant: 'accent',
  },
  render: (args) => ({
    components: { UiBadge },
    setup() {
      return { args }
    },
    template: '<UiBadge v-bind="args">Accent Badge</UiBadge>',
  }),
}

export const Success: Story = {
  args: {
    variant: 'success',
  },
  render: (args) => ({
    components: { UiBadge },
    setup() {
      return { args }
    },
    template: '<UiBadge v-bind="args">Success Badge</UiBadge>',
  }),
}

export const Warning: Story = {
  args: {
    variant: 'warning',
  },
  render: (args) => ({
    components: { UiBadge },
    setup() {
      return { args }
    },
    template: '<UiBadge v-bind="args">Warning Badge</UiBadge>',
  }),
}

export const Danger: Story = {
  args: {
    variant: 'danger',
  },
  render: (args) => ({
    components: { UiBadge },
    setup() {
      return { args }
    },
    template: '<UiBadge v-bind="args">Danger Badge</UiBadge>',
  }),
}

export const Info: Story = {
  args: {
    variant: 'info',
  },
  render: (args) => ({
    components: { UiBadge },
    setup() {
      return { args }
    },
    template: '<UiBadge v-bind="args">Info Badge</UiBadge>',
  }),
}

export const Small: Story = {
  args: {
    size: 'sm',
  },
  render: (args) => ({
    components: { UiBadge },
    setup() {
      return { args }
    },
    template: '<UiBadge v-bind="args">Small Badge</UiBadge>',
  }),
}

export const Medium: Story = {
  args: {
    size: 'md',
  },
  render: (args) => ({
    components: { UiBadge },
    setup() {
      return { args }
    },
    template: '<UiBadge v-bind="args">Medium Badge</UiBadge>',
  }),
}
