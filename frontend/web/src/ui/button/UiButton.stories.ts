import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiButton from './UiButton.vue'

const meta: Meta<typeof UiButton> = {
  title: 'Ui/Button',
  component: UiButton,
  argTypes: {
    variant: {
      control: 'select',
      options: ['primary', 'secondary', 'ghost', 'danger'],
    },
    size: {
      control: 'select',
      options: ['sm', 'md', 'icon'],
    },
    disabled: {
      control: 'boolean',
    },
    loading: {
      control: 'boolean',
    },
  },
}

export default meta
type Story = StoryObj<typeof UiButton>

export const Primary: Story = {
  args: {
    variant: 'primary',
  },
  render: (args) => ({
    components: { UiButton },
    setup() {
      return { args }
    },
    template: '<UiButton v-bind="args">Primary Button</UiButton>',
  }),
}

export const Secondary: Story = {
  args: {
    variant: 'secondary',
  },
  render: (args) => ({
    components: { UiButton },
    setup() {
      return { args }
    },
    template: '<UiButton v-bind="args">Secondary Button</UiButton>',
  }),
}

export const Ghost: Story = {
  args: {
    variant: 'ghost',
  },
  render: (args) => ({
    components: { UiButton },
    setup() {
      return { args }
    },
    template: '<UiButton v-bind="args">Ghost Button</UiButton>',
  }),
}

export const Danger: Story = {
  args: {
    variant: 'danger',
  },
  render: (args) => ({
    components: { UiButton },
    setup() {
      return { args }
    },
    template: '<UiButton v-bind="args">Danger Button</UiButton>',
  }),
}

export const Small: Story = {
  args: {
    size: 'sm',
  },
  render: (args) => ({
    components: { UiButton },
    setup() {
      return { args }
    },
    template: '<UiButton v-bind="args">Small Button</UiButton>',
  }),
}

export const Medium: Story = {
  args: {
    size: 'md',
  },
  render: (args) => ({
    components: { UiButton },
    setup() {
      return { args }
    },
    template: '<UiButton v-bind="args">Medium Button</UiButton>',
  }),
}

export const Icon: Story = {
  args: {
    size: 'icon',
    'aria-label': 'Settings',
  },
  render: (args) => ({
    components: { UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiButton v-bind="args">
        <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
        </svg>
      </UiButton>
    `,
  }),
}

export const Disabled: Story = {
  args: {
    disabled: true,
  },
  render: (args) => ({
    components: { UiButton },
    setup() {
      return { args }
    },
    template: '<UiButton v-bind="args">Disabled Button</UiButton>',
  }),
}

export const Loading: Story = {
  args: {
    loading: true,
  },
  render: (args) => ({
    components: { UiButton },
    setup() {
      return { args }
    },
    template: '<UiButton v-bind="args">Loading Button</UiButton>',
  }),
}
