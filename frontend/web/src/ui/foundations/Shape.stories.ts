import type { Meta, StoryObj } from '@storybook/vue3-vite'

const meta: Meta = {
  title: 'Foundations/Shape',
  parameters: {
    layout: 'fullscreen',
  },
}

export default meta
type Story = StoryObj

const radii = [
  { name: 'sm', value: '4px', className: 'rounded-sm' },
  { name: 'control', value: '8px', className: 'rounded-control' },
  { name: 'panel', value: '12px', className: 'rounded-panel' },
]

const shadows = [
  { name: 'xs', elevation: '0 1px 2px (6%)', className: 'shadow-xs' },
  { name: 'sm', elevation: '0 2px 4px (8%)', className: 'shadow-sm' },
  { name: 'md', elevation: '0 4px 12px (12%)', className: 'shadow-md' },
  { name: 'lg', elevation: '0 12px 32px (20%)', className: 'shadow-lg' },
  { name: 'xl', elevation: '0 24px 60px (33%)', className: 'shadow-xl' },
]

const spacing = [
  { step: 1, rem: '0.25rem', px: '4px', className: 'w-1' },
  { step: 2, rem: '0.5rem', px: '8px', className: 'w-2' },
  { step: 3, rem: '0.75rem', px: '12px', className: 'w-3' },
  { step: 4, rem: '1rem', px: '16px', className: 'w-4' },
  { step: 5, rem: '1.25rem', px: '20px', className: 'w-5' },
  { step: 6, rem: '1.5rem', px: '24px', className: 'w-6' },
  { step: 7, rem: '1.75rem', px: '28px', className: 'w-7' },
  { step: 8, rem: '2rem', px: '32px', className: 'w-8' },
]

export const Default: Story = {
  render: () => ({
    setup() {
      return { radii, shadows, spacing }
    },
    template: `
      <div class="p-6 max-w-5xl mx-auto font-sans text-foreground bg-background space-y-10">
        <div>
          <div role="heading" aria-level="1" class="text-2xl font-bold mb-2">Shape &amp; Spacing</div>
          <div class="text-sm text-muted-foreground">Border radii, card shadows, and spacing scale steps 1 to 8.</div>
        </div>

        <!-- Radii -->
        <section>
          <div role="heading" aria-level="2" class="text-lg font-semibold mb-4 pb-2 border-b border-border">Corner Radii</div>
          <div class="grid grid-cols-1 sm:grid-cols-3 gap-4">
            <div
              v-for="r in radii"
              :key="r.name"
              class="flex flex-col items-center p-4 border border-border bg-card rounded-panel shadow-xs"
            >
              <div
                class="w-16 h-16 border-2 border-primary bg-subtle mb-3"
                :class="r.className"
                aria-hidden="true"
              />
              <span class="font-mono text-xs font-semibold">radius-{{ r.name }}</span>
              <span class="text-2xs text-muted-foreground">{{ r.value }}</span>
            </div>
          </div>
        </section>

        <!-- Shadows -->
        <section>
          <div role="heading" aria-level="2" class="text-lg font-semibold mb-4 pb-2 border-b border-border">Card Shadows</div>
          <div class="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-6">
            <div
              v-for="s in shadows"
              :key="s.name"
              class="p-6 bg-card rounded-panel border border-border"
              :class="s.className"
            >
              <span class="font-mono text-xs font-semibold block mb-1">shadow-{{ s.name }}</span>
              <div class="text-xs text-muted-foreground">
                {{ s.elevation }}
              </div>
            </div>
          </div>
        </section>

        <!-- Spacing -->
        <section>
          <div role="heading" aria-level="2" class="text-lg font-semibold mb-4 pb-2 border-b border-border">Spacing (Steps 1 to 8)</div>
          <div class="border border-border rounded-panel overflow-hidden bg-card shadow-xs">
            <table class="w-full text-left border-collapse">
              <caption class="sr-only">Spacing scale steps 1 through 8</caption>
              <thead>
                <tr class="border-b border-border bg-subtle text-foreground">
                  <th scope="col"><span class="font-semibold text-xs uppercase tracking-wider text-foreground">Step</span></th>
                  <th scope="col"><span class="font-semibold text-xs uppercase tracking-wider text-foreground">Size (rem / px)</span></th>
                  <th scope="col"><span class="font-semibold text-xs uppercase tracking-wider text-foreground">Visual</span></th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border">
                <tr v-for="s in spacing" :key="s.step" class="hover:bg-hover transition-colors">
                  <td class="font-mono"><span class="text-xs font-semibold text-foreground">{{ s.step }}</span></td>
                  <td class="font-mono"><span class="text-xs text-muted-foreground">{{ s.rem }} ({{ s.px }})</span></td>
                  <td>
                    <div class="h-4 bg-accent rounded-sm" :class="s.className" aria-hidden="true" />
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </div>
    `,
  }),
}
