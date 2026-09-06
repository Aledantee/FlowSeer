<script setup lang="ts">
withDefaults(
  defineProps<{
    variant?: 'primary' | 'secondary' | 'ghost'
    size?: 'small' | 'medium'
    type?: 'button' | 'submit' | 'reset'
  }>(),
  { variant: 'secondary', size: 'medium', type: 'button' },
)
</script>

<template>
  <button
    :type="type"
    :class="['ui-button', `ui-button--${variant}`, `ui-button--${size}`]"
  >
    <slot />
  </button>
</template>
