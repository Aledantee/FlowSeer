import type { Meta, StoryObj } from '@storybook/vue3-vite'

const meta: Meta = {
  title: 'Foundations/Typography',
  parameters: {
    layout: 'fullscreen',
  },
}

export default meta
type Story = StoryObj

const steps = [
  {
    name: '2xs',
    size: '0.6875rem (11px)',
    lineHeight: '0.875rem (14px)',
    className: 'text-2xs',
  },
  {
    name: 'xs',
    size: '0.75rem (12px)',
    lineHeight: '1rem (16px)',
    className: 'text-xs',
  },
  {
    name: 'sm',
    size: '0.8125rem (13px)',
    lineHeight: '1.125rem (18px)',
    className: 'text-sm',
  },
  {
    name: 'base',
    size: '0.875rem (14px)',
    lineHeight: '1.25rem (20px)',
    className: 'text-base',
  },
  {
    name: 'lg',
    size: '1rem (16px)',
    lineHeight: '1.5rem (24px)',
    className: 'text-lg',
  },
  {
    name: 'xl',
    size: '1.125rem (18px)',
    lineHeight: '1.625rem (26px)',
    className: 'text-xl',
  },
  {
    name: '2xl',
    size: '1.375rem (22px)',
    lineHeight: '1.75rem (28px)',
    className: 'text-2xl',
  },
  {
    name: '3xl',
    size: '1.75rem (28px)',
    lineHeight: '2.125rem (34px)',
    className: 'text-3xl',
  },
]

export const Default: Story = {
  render: () => ({
    setup() {
      return { steps }
    },
    template: `
      <div class="p-6 max-w-5xl mx-auto font-sans text-[var(--foreground)] bg-[var(--background)]">
        <h1 class="text-2xl font-bold mb-2">Typography</h1>
        <p class="text-sm text-[var(--muted-foreground)] mb-6">Type scale, fonts (Inter, Mono), and tabular figures.</p>

        <div class="space-y-6">
          <div
            v-for="step in steps"
            :key="step.name"
            class="p-4 border border-[var(--border)] rounded-lg bg-[var(--card)] shadow-xs"
          >
            <div class="flex items-center justify-between pb-3 mb-3 border-b border-[var(--border)] text-xs text-[var(--muted-foreground)]">
              <span class="font-mono font-semibold text-[var(--foreground)]">text-{{ step.name }}</span>
              <span>Size: {{ step.size }} | Line height: {{ step.lineHeight }}</span>
            </div>

            <div class="space-y-3">
              <div>
                <span class="text-2xs uppercase tracking-wider text-[var(--muted-foreground)] block mb-1">Inter (Sans)</span>
                <p :class="[step.className, 'font-sans text-[var(--foreground)]']">
                  Sphinx of black quartz, judge my vow.
                </p>
              </div>

              <div>
                <span class="text-2xs uppercase tracking-wider text-[var(--muted-foreground)] block mb-1">Mono</span>
                <p :class="[step.className, 'font-mono text-[var(--foreground)]']">
                  interface eth0/1: up (10 Gbps full-duplex)
                </p>
              </div>

              <div>
                <span class="text-2xs uppercase tracking-wider text-[var(--muted-foreground)] block mb-1">Tabular Figures</span>
                <p :class="[step.className, 'font-mono tabular-nums text-[var(--foreground)]']">
                  99,842,105 pkts/s &middot; 42.871 Gbps &middot; latency 0.124 ms
                </p>
              </div>
            </div>
          </div>
        </div>
      </div>
    `,
  }),
}
