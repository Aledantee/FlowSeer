<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch, nextTick } from 'vue'
const props = defineProps<{ name: string; iconUrl?: string }>()
const label = ref<HTMLSpanElement>()
const text = ref<HTMLSpanElement>()
const truncated = ref(false)
const failed = ref(false)
let observer: ResizeObserver | undefined
function measure() {
  truncated.value =
    !!label.value &&
    !!text.value &&
    text.value.scrollWidth > label.value.clientWidth
}
onMounted(() => {
  observer = new ResizeObserver(measure)
  if (label.value) observer.observe(label.value)
  if (text.value) observer.observe(text.value)
  measure()
})
onUnmounted(() => observer?.disconnect())
watch(
  () => [props.name, props.iconUrl],
  async () => {
    failed.value = false
    await nextTick()
    measure()
  },
)
</script>

<template>
  <span ref="label" class="tenant-label" :title="name" :aria-label="name">
    <span
      ref="text"
      class="tenant-label-text"
      :class="{ 'tenant-label-measure': truncated && iconUrl && !failed }"
      :aria-hidden="truncated && !!iconUrl && !failed"
      >{{ name }}</span
    >
    <img
      v-if="truncated && iconUrl && !failed"
      :src="iconUrl"
      alt=""
      class="tenant-label-icon"
      @error="failed = true"
    />
  </span>
</template>
