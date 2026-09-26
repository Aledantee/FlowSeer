<script setup lang="ts">
import { computed, provide, useId } from 'vue'

export interface UiFieldProps {
  id?: string
  label?: string
  description?: string
  error?: string
  required?: boolean
}

const props = withDefaults(defineProps<UiFieldProps>(), {
  id: undefined,
  label: undefined,
  description: undefined,
  error: undefined,
  required: false,
})

const autoId = useId()
const fieldId = computed(() => props.id || autoId)
const descriptionId = computed(() => `${fieldId.value}-desc`)
const errorId = computed(() => `${fieldId.value}-err`)

const computedDescribedBy = computed(() => {
  const ids: string[] = []
  if (props.description) ids.push(descriptionId.value)
  if (props.error) ids.push(errorId.value)
  return ids.length > 0 ? ids.join(' ') : undefined
})

const isInvalid = computed(() => Boolean(props.error))

provide('ui-field-context', {
  id: fieldId,
  describedBy: computedDescribedBy,
  invalid: isInvalid,
})
</script>

<template>
  <div class="flex flex-col gap-1.5 w-full">
    <label
      v-if="label"
      :for="fieldId"
      class="text-xs font-medium text-foreground select-none"
    >
      {{ label }}
      <span
        v-if="required"
        class="text-danger-foreground ml-0.5"
        aria-hidden="true"
        >*</span
      >
    </label>
    <slot
      :id="fieldId"
      :described-by="computedDescribedBy"
      :invalid="isInvalid"
    />
    <p
      v-if="description"
      :id="descriptionId"
      class="text-xs text-muted-foreground m-0"
    >
      {{ description }}
    </p>
    <p
      v-if="error"
      :id="errorId"
      role="alert"
      class="text-xs text-danger-foreground m-0"
    >
      {{ error }}
    </p>
  </div>
</template>
