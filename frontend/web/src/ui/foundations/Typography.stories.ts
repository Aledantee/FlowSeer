import type { Meta, StoryObj } from '@storybook/vue3-vite'
import { onMounted, ref } from 'vue'

const meta: Meta = {
  title: 'Foundations/Typography',
  parameters: {
    layout: 'fullscreen',
  },
}

export default meta
type Story = StoryObj

interface TypographyStep {
  name: string
  size: string
  lineHeight: string
  className: string
}

const defaultSteps: TypographyStep[] = [
  { name: '2xs', size: '10px', lineHeight: '14px', className: 'text-2xs' },
  { name: 'xs', size: '11px', lineHeight: '16px', className: 'text-xs' },
  { name: 'sm', size: '12px', lineHeight: '16px', className: 'text-sm' },
  { name: 'base', size: '13px', lineHeight: '20px', className: 'text-base' },
  { name: 'md', size: '14px', lineHeight: '20px', className: 'text-md' },
  { name: 'lg', size: '16px', lineHeight: '24px', className: 'text-lg' },
  { name: 'xl', size: '20px', lineHeight: '28px', className: 'text-xl' },
  { name: '2xl', size: '24px', lineHeight: '32px', className: 'text-2xl' },
  { name: '3xl', size: '28px', lineHeight: '36px', className: 'text-3xl' },
]

export const Default: Story = {
  render: () => ({
    setup() {
      const steps = ref<TypographyStep[]>(defaultSteps)

      onMounted(() => {
        const rootStyle = getComputedStyle(document.documentElement)
        steps.value = defaultSteps.map((s) => {
          const computedSize = rootStyle
            .getPropertyValue(`--text-${s.name}`)
            .trim()
          const computedLineHeight = rootStyle
            .getPropertyValue(`--text-${s.name}--line-height`)
            .trim()
          return {
            name: s.name,
            size: computedSize || s.size,
            lineHeight: computedLineHeight || s.lineHeight,
            className: s.className,
          }
        })
      })

      return { steps }
    },
    template: `
      <div class="p-6 max-w-5xl mx-auto font-sans text-foreground bg-background">
        <div role="heading" aria-level="1" class="text-2xl font-bold mb-2">Typography</div>
        <div class="text-sm text-muted-foreground mb-6">Type scale, fonts (Inter, Mono), and tabular figures.</div>

        <div class="space-y-6">
          <div
            v-for="step in steps"
            :key="step.name"
            class="p-4 border border-border rounded-panel bg-card shadow-xs"
          >
            <div class="flex items-center justify-between pb-3 mb-3 border-b border-border text-xs text-muted-foreground">
              <span class="font-mono font-semibold text-foreground">text-{{ step.name }}</span>
              <span>Size: {{ step.size }} | Line height: {{ step.lineHeight }}</span>
            </div>

            <div class="space-y-3">
              <div>
                <span class="text-2xs uppercase tracking-wider text-muted-foreground block mb-1">Inter (Sans)</span>
                <div :class="[step.className, 'font-sans text-foreground']">
                  Sphinx of black quartz, judge my vow.
                </div>
              </div>

              <div>
                <span class="text-2xs uppercase tracking-wider text-muted-foreground block mb-1">Mono</span>
                <div :class="[step.className, 'font-mono text-foreground']">
                  interface eth0/1: up (10 Gbps full-duplex)
                </div>
              </div>

              <div>
                <span class="text-2xs uppercase tracking-wider text-muted-foreground block mb-1">Tabular Figures</span>
                <div :class="[step.className, 'font-mono tabular-nums text-foreground']">
                  99,842,105 pkts/s &middot; 42.871 Gbps &middot; latency 0.124 ms
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    `,
  }),
}
