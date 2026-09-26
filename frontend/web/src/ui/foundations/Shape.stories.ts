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
  { name: 'xs', value: '2px', className: 'rounded-xs' },
  { name: 'sm', value: '4px', className: 'rounded-sm' },
  { name: 'md', value: '6px', className: 'rounded-md' },
  { name: 'lg', value: '8px', className: 'rounded-lg' },
  { name: 'xl', value: '12px', className: 'rounded-xl' },
  { name: 'full', value: '9999px', className: 'rounded-full' },
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
      return { radii, spacing }
    },
    template: `
      <div class="p-6 max-w-5xl mx-auto font-sans text-[var(--foreground)] bg-[var(--background)] space-y-10">
        <div>
          <h1 class="text-2xl font-bold mb-2">Shape &amp; Spacing</h1>
          <p class="text-sm text-[var(--muted-foreground)]">Border radii, card shadows, and spacing scale steps 1 to 8.</p>
        </div>

        <!-- Radii -->
        <section>
          <h2 class="text-lg font-semibold mb-4 pb-2 border-b border-[var(--border)]">Corner Radii</h2>
          <div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-6 gap-4">
            <div
              v-for="r in radii"
              :key="r.name"
              class="flex flex-col items-center p-4 border border-[var(--border)] bg-[var(--card)] rounded-lg shadow-xs"
            >
              <div
                class="w-16 h-16 border-2 border-[var(--primary)] bg-[var(--subtle)] mb-3"
                :class="r.className"
                aria-hidden="true"
              />
              <span class="font-mono text-xs font-semibold">radius-{{ r.name }}</span>
              <span class="text-2xs text-[var(--muted-foreground)]">{{ r.value }}</span>
            </div>
          </div>
        </section>

        <!-- Shadows -->
        <section>
          <h2 class="text-lg font-semibold mb-4 pb-2 border-b border-[var(--border)]">Card Shadows</h2>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-6">
            <div class="p-6 bg-[var(--card)] rounded-lg border border-[var(--border)] shadow-[var(--shadow-card)]">
              <span class="font-mono text-xs font-semibold block mb-1">--shadow-card</span>
              <p class="text-xs text-[var(--muted-foreground)]">
                Subtle elevation for cards and panels against the background canvas.
              </p>
            </div>
            <div class="p-6 bg-[var(--card)] rounded-lg border border-[var(--border)] shadow-[var(--shadow-popover)]">
              <span class="font-mono text-xs font-semibold block mb-1">--shadow-popover</span>
              <p class="text-xs text-[var(--muted-foreground)]">
                Higher elevation for menus, popovers, and elevated overlays.
              </p>
            </div>
          </div>
        </section>

        <!-- Spacing -->
        <section>
          <h2 class="text-lg font-semibold mb-4 pb-2 border-b border-[var(--border)]">Spacing (Steps 1 to 8)</h2>
          <div class="border border-[var(--border)] rounded-lg overflow-hidden bg-[var(--card)] shadow-xs">
            <table class="w-full text-left border-collapse">
              <caption class="sr-only">Spacing scale steps 1 through 8</caption>
              <thead>
                <tr class="border-b border-[var(--border)] bg-[var(--subtle)] text-[var(--foreground)]">
                  <th scope="col" class="py-2.5 px-4 font-semibold text-xs uppercase tracking-wider">Step</th>
                  <th scope="col" class="py-2.5 px-4 font-semibold text-xs uppercase tracking-wider">Size (rem / px)</th>
                  <th scope="col" class="py-2.5 px-4 font-semibold text-xs uppercase tracking-wider">Visual</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-[var(--border)]">
                <tr v-for="s in spacing" :key="s.step" class="hover:bg-[var(--hover)] transition-colors">
                  <td class="py-2.5 px-4 font-mono text-xs font-semibold">{{ s.step }}</td>
                  <td class="py-2.5 px-4 font-mono text-xs text-[var(--muted-foreground)]">{{ s.rem }} ({{ s.px }})</td>
                  <td class="py-2.5 px-4">
                    <div class="h-4 bg-[var(--accent)] rounded-xs" :class="s.className" aria-hidden="true" />
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
