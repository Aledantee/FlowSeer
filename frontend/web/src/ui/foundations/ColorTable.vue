<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
import sourceData from '../../../design/palette-source.json'
import {
  resolve,
  toSrgb,
  contrast,
  pairs,
  type Theme,
  type PaletteSource,
} from '../../theme/palette.ts'

const source = sourceData as unknown as PaletteSource
const currentTheme = ref<Theme>('light')
let observer: MutationObserver | null = null

onMounted(() => {
  const getAttr = (): Theme =>
    document.documentElement.getAttribute('data-theme') === 'dark'
      ? 'dark'
      : 'light'
  currentTheme.value = getAttr()

  observer = new MutationObserver(() => {
    currentTheme.value = getAttr()
  })

  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['data-theme'],
  })
})

onUnmounted(() => {
  observer?.disconnect()
  observer = null
})

interface GatedPairInfo {
  fg: string
  bg: string
  minRatio: number
  ratio: string
  passes: boolean
}

interface TokenInfo {
  name: string
  step: string
  gatedPairs: GatedPairInfo[]
}

const tokens = computed<TokenInfo[]>(() => {
  const active = currentTheme.value
  return Object.entries(source.semantic).map(([name, mapping]) => {
    const rawStep = mapping[active]
    const step =
      typeof rawStep === 'string'
        ? rawStep
        : `${rawStep.ref} (${Math.round(rawStep.alpha * 100)}%)`

    const matchingPairs = pairs.filter(([fg, bg]) => fg === name || bg === name)
    const gatedPairs: GatedPairInfo[] = matchingPairs.map(
      ([fg, bg, minRatio]) => {
        const fgRgb = toSrgb(resolve(source, fg, active))
        const bgRgb = toSrgb(resolve(source, bg, active))
        const r = contrast(fgRgb, bgRgb)
        return {
          fg,
          bg,
          minRatio,
          ratio: r.toFixed(2),
          passes: r >= minRatio,
        }
      },
    )

    return {
      name,
      step,
      gatedPairs,
    }
  })
})
</script>

<template>
  <div
    class="color-table-wrapper p-6 max-w-5xl mx-auto font-sans text-sm text-[var(--foreground)] bg-[var(--background)]"
  >
    <div class="mb-6 flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-[var(--foreground)]">
          Semantic Colors
        </h1>
        <p class="text-sm text-[var(--muted-foreground)] mt-1">
          Active theme:
          <span
            class="font-semibold uppercase tracking-wider text-[var(--foreground)]"
            >{{ currentTheme }}</span
          >
        </p>
      </div>
    </div>

    <div
      class="border border-[var(--border)] rounded-lg overflow-hidden bg-[var(--card)] shadow-xs"
    >
      <table class="w-full text-left border-collapse">
        <caption class="sr-only">
          Semantic token palette and contrast audit
        </caption>
        <thead>
          <tr
            class="border-b border-[var(--border)] bg-[var(--subtle)] text-[var(--foreground)]"
          >
            <th
              scope="col"
              class="py-3 px-4 font-semibold text-xs uppercase tracking-wider"
            >
              Token
            </th>
            <th
              scope="col"
              class="py-3 px-4 font-semibold text-xs uppercase tracking-wider"
            >
              Swatch
            </th>
            <th
              scope="col"
              class="py-3 px-4 font-semibold text-xs uppercase tracking-wider"
            >
              Active Step
            </th>
            <th
              scope="col"
              class="py-3 px-4 font-semibold text-xs uppercase tracking-wider"
            >
              Gated Contrast Pairs
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-[var(--border)]">
          <tr
            v-for="token in tokens"
            :key="token.name"
            class="hover:bg-[var(--hover)] transition-colors"
          >
            <td class="py-3 px-4 font-mono text-xs">--{{ token.name }}</td>
            <td class="py-3 px-4">
              <div
                class="w-8 h-8 rounded-md border border-[var(--border)] shadow-xs"
                :style="{ backgroundColor: `var(--${token.name})` }"
                aria-hidden="true"
              />
            </td>
            <td
              class="py-3 px-4 font-mono text-xs text-[var(--muted-foreground)]"
            >
              {{ token.step }}
            </td>
            <td class="py-3 px-4 text-xs">
              <ul
                v-if="token.gatedPairs.length > 0"
                class="space-y-1 list-none p-0 m-0"
              >
                <li
                  v-for="pair in token.gatedPairs"
                  :key="`${pair.fg}-${pair.bg}`"
                  class="flex items-center gap-2"
                >
                  <span
                    class="inline-block px-1.5 py-0.5 rounded font-mono text-2xs"
                    :class="
                      pair.passes
                        ? 'bg-[var(--success-surface)] text-[var(--success-foreground)] border border-[var(--success-border)]'
                        : 'bg-[var(--danger-surface)] text-[var(--danger-foreground)] border border-[var(--danger-border)]'
                    "
                  >
                    {{ pair.ratio }}:1
                  </span>
                  <span class="text-[var(--muted-foreground)]">
                    <span
                      :class="
                        pair.fg === token.name
                          ? 'font-bold text-[var(--foreground)]'
                          : ''
                      "
                      >{{ pair.fg }}</span
                    >
                    on
                    <span
                      :class="
                        pair.bg === token.name
                          ? 'font-bold text-[var(--foreground)]'
                          : ''
                      "
                      >{{ pair.bg }}</span
                    >
                    (min {{ pair.minRatio }}:1)
                  </span>
                </li>
              </ul>
              <span v-else class="text-[var(--muted-foreground)] italic">
                None
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
