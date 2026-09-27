<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import type { TrafficPoint } from '../domain/overview'
const props = defineProps<{ points: TrafficPoint[]; label: string }>()
const frame = ref<HTMLElement>()
const width = ref(640)
const height = 200
const pad = { top: 12, right: 12, bottom: 24, left: 44 }
const active = ref<number>()
let observer: ResizeObserver | undefined
onMounted(() => {
  observer = new ResizeObserver(([entry]) => {
    if (entry) width.value = Math.max(240, entry.contentRect.width)
  })
  if (frame.value) observer.observe(frame.value)
})
onUnmounted(() => observer?.disconnect())

// Round the axis top up to a step of 1, 2, or 5 × 10ⁿ so gridlines land on
// readable values.
const ceiling = computed(() => {
  const max = Math.max(1, ...props.points.map((point) => point.mbps))
  const magnitude = 10 ** Math.floor(Math.log10(max))
  const step = [1, 2, 5, 10].find((f) => f * magnitude >= max) ?? 10
  return step * magnitude
})
const ticks = computed(() => [0, 0.5, 1].map((f) => f * ceiling.value))
const x = (index: number) =>
  pad.left +
  (index * (width.value - pad.left - pad.right)) /
    Math.max(1, props.points.length - 1)
const y = (mbps: number) =>
  pad.top + (1 - mbps / ceiling.value) * (height - pad.top - pad.bottom)
const line = computed(() =>
  props.points
    .map(
      (point, index) =>
        `${index ? 'L' : 'M'}${x(index).toFixed(1)},${y(point.mbps).toFixed(1)}`,
    )
    .join(''),
)
const area = computed(
  () => `${line.value}L${x(props.points.length - 1)},${y(0)}L${x(0)},${y(0)}Z`,
)
const hourLabel = (hour: number) => `${String(hour).padStart(2, '0')}:00`
const current = computed(() =>
  active.value === undefined ? undefined : props.points[active.value],
)

function track(event: PointerEvent) {
  const box = frame.value?.getBoundingClientRect()
  if (!box) return
  const step = (width.value - pad.left - pad.right) / (props.points.length - 1)
  const index = Math.round((event.clientX - box.left - pad.left) / step)
  active.value = Math.min(props.points.length - 1, Math.max(0, index))
}
function step(event: KeyboardEvent) {
  const last = props.points.length - 1
  const from = active.value ?? last
  const next =
    event.key === 'ArrowLeft'
      ? from - 1
      : event.key === 'ArrowRight'
        ? from + 1
        : event.key === 'Home'
          ? 0
          : event.key === 'End'
            ? last
            : undefined
  if (next === undefined) return
  event.preventDefault()
  active.value = Math.min(last, Math.max(0, next))
}
</script>

<template>
  <figure class="traffic-chart">
    <div
      class="traffic-legend flex items-center gap-2 text-xs text-muted-foreground mb-2"
    >
      <span
        class="inline-block w-3 h-0.5 rounded-full"
        style="background-color: var(--chart-1)"
        aria-hidden="true"
      />
      <span class="font-medium text-foreground">{{ label }}</span>
    </div>
    <div
      ref="frame"
      class="traffic-frame"
      tabindex="0"
      role="img"
      :aria-label="`${label}. Use the arrow keys to read hourly values.`"
      @pointermove="track"
      @pointerleave="active = undefined"
      @keydown="step"
      @blur="active = undefined"
    >
      <svg :width="width" :height="height" aria-hidden="true">
        <g class="traffic-grid">
          <template v-for="tick in ticks" :key="tick">
            <line
              :x1="pad.left"
              :x2="width - pad.right"
              :y1="y(tick)"
              :y2="y(tick)"
            />
            <text :x="pad.left - 8" :y="y(tick)" dy="0.32em" text-anchor="end">
              {{ tick }}
            </text>
          </template>
          <template v-for="(point, index) in points" :key="index">
            <text
              v-if="index % 6 === 0 || index === points.length - 1"
              :x="x(index)"
              :y="height - 6"
              :text-anchor="index === points.length - 1 ? 'end' : 'middle'"
            >
              {{ index === points.length - 1 ? 'Now' : hourLabel(point.hour) }}
            </text>
          </template>
        </g>
        <path class="traffic-area" :d="area" />
        <path class="traffic-line" :d="line" />
        <g v-if="current && active !== undefined">
          <line
            class="traffic-crosshair"
            :x1="x(active)"
            :x2="x(active)"
            :y1="pad.top"
            :y2="y(0)"
          />
          <circle
            class="traffic-dot"
            :cx="x(active)"
            :cy="y(current.mbps)"
            r="4"
          />
        </g>
      </svg>
      <div
        v-if="current && active !== undefined"
        class="traffic-tooltip"
        :style="{
          left: `${x(active)}px`,
          top: `${y(current.mbps)}px`,
        }"
        :class="{ flip: x(active) > width * 0.7 }"
      >
        <small>{{
          active === points.length - 1 ? 'Now' : hourLabel(current.hour)
        }}</small>
        <strong>{{ current.mbps }} <span>Mbps</span></strong>
      </div>
    </div>
    <table class="sr-only">
      <caption>
        {{
          label
        }}
      </caption>
      <thead>
        <tr>
          <th scope="col">Hour</th>
          <th scope="col">Traffic (Mbps)</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(point, index) in points" :key="index">
          <td>{{ hourLabel(point.hour) }}</td>
          <td>{{ point.mbps }}</td>
        </tr>
      </tbody>
    </table>
  </figure>
</template>

<style scoped>
.traffic-chart {
  margin: 0;
  padding: 0 12px 12px;
}

.traffic-frame {
  position: relative;
  outline-offset: -3px;
  touch-action: pan-y;
}

.traffic-frame svg {
  display: block;
}

.traffic-grid line {
  stroke: var(--border);
  stroke-width: 1;
  stroke-dasharray: 2 3;
}

.traffic-grid text {
  fill: var(--muted-foreground);
  font-size: var(--text-2xs);
  font-variant-numeric: tabular-nums;
}

.traffic-area {
  fill: color-mix(in srgb, var(--chart-1) 12%, transparent);
}

.traffic-line {
  fill: none;
  stroke: var(--chart-1);
  stroke-width: 2;
  stroke-linejoin: round;
  stroke-linecap: round;
}

.traffic-crosshair {
  stroke: var(--chart-1);
  stroke-width: 1;
}

.traffic-dot {
  fill: var(--chart-1);
  stroke: var(--card);
  stroke-width: 2;
}

.traffic-tooltip {
  position: absolute;
  transform: translate(12px, -50%);
  pointer-events: none;
  background: var(--card-header);
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  padding: 6px 10px;
  box-shadow: var(--shadow-md);
  white-space: nowrap;
}

.traffic-tooltip.flip {
  transform: translate(calc(-100% - 12px), -50%);
}

.traffic-tooltip small {
  font-size: var(--text-2xs);
}

.traffic-tooltip strong {
  font-size: var(--text-md);
  font-weight: 550;
  font-variant-numeric: tabular-nums;
}

.traffic-tooltip strong span {
  font-size: var(--text-2xs);
  color: var(--muted-foreground);
  font-weight: 400;
}
</style>
