---
title: A Headless Form Control Needs a Microtask Reset Listener to Synchronize State and DOM
date: 2026-09-27
last_verified: 2026-09-27
category: architecture-patterns
module: frontend/web/src/ui/form
problem_type: architecture_pattern
component: form_controls
severity: high
applies_when:
  - "Wrapping headless UI primitives into form controls that participate in HTML forms"
  - "Investigating why native form reset leaves custom components or Vue v-model dirty"
  - "Supporting both controlled (v-model) and uncontrolled (name) usage in reusable form components"
related_components: [web_ui, vue_components]
tags: [form-reset, headless-ui, reka-ui, v-model, controlled-vs-uncontrolled, microtask, vue]
---

# A headless form control needs a microtask reset listener to synchronize state and DOM

## The situation

Web components wrapping headless primitives (such as Reka UI) often appear to
handle HTML form submission through hidden inputs or root props (`name`,
`required`). When a user triggers `<form @reset>` or `form.reset()`, three
subtle synchronization failures surface:

1. Native inputs (`<input>`, `<textarea>`) revert their `.value` in the DOM,
   but HTML form reset dispatches neither `input` nor `change` events. Vue's
   `v-model` binding does not update, leaving application state dirty while the
   screen looks clean.
2. Headless primitives (like Reka's button-based Switch, Checkbox, RadioGroup,
   and Select) do not listen to enclosing `<form>` reset events. Their visual
   DOM state (`data-state="checked"`, trigger text) remains stuck on the modified
   value.
3. If an input wrapper defines a default model value in `withDefaults`
   (`modelValue: ''` or `modelValue: false`), `props.modelValue` is never
   `undefined`. The component becomes permanently controlled, breaking native
   uncontrolled form fields.

## What is true and why

1. **Defer reset callbacks to a microtask.** The DOM `reset` event is
   cancelable. If a listener calls `event.preventDefault()`, the form must not
   reset. Using `queueMicrotask` ensures the handler verifies
   `!event.defaultPrevented` and runs after browser default restoration completes.
2. **Leave default model values unset.** Omit default prop values in
   `withDefaults`. A component detects controlled mode when
   `props.modelValue !== undefined` and uncontrolled mode otherwise.
3. **Handle transitions to undefined.** When watching `props.modelValue`, map
   `undefined` back to the default empty value (`val ?? ''` or `val ?? false`).
   Ignoring `undefined` leaves internal state stuck when a parent unsets a
   controlled value.
4. **Restore mount state on reset.** Capture `initialValue` on mount. On form
   reset, restore internal state and emit `update:modelValue` with that initial
   value so Vue reactive state matches the DOM.

## Working example

In `frontend/web/src/ui/form/useFormReset.ts:23-50`, the helper resolves the
enclosing form element and defers the reset callback:

```ts
export function useFormReset(options: UseFormResetOptions): void {
  let form: HTMLFormElement | null = null

  function handleReset(event: Event): void {
    queueMicrotask(() => {
      if (event.defaultPrevented) return
      options.onReset()
    })
  }

  onMounted(() => {
    const el = resolveElement(options.elementRef.value)
    if (el) {
      form = el.closest('form')
      if (form) form.addEventListener('reset', handleReset)
    }
  })

  onUnmounted(() => {
    if (form) {
      form.removeEventListener('reset', handleReset)
      form = null
    }
  })
}
```

In `frontend/web/src/ui/form/UiSelect.vue:97-126`, the component tracks controlled
versus uncontrolled state and uses the reset hook:

```ts
const internalValue = ref<string>(props.modelValue ?? '')

watch(
  () => props.modelValue,
  (val) => {
    internalValue.value = val ?? ''
  },
)

const currentValue = computed<string>(() =>
  props.modelValue !== undefined ? props.modelValue : internalValue.value,
)

let initialValue = ''
onMounted(() => {
  initialValue =
    props.modelValue !== undefined ? props.modelValue : internalValue.value
})

useFormReset({
  elementRef: triggerRef,
  onReset: () => {
    internalValue.value = initialValue
    emit('update:modelValue', initialValue)
  },
})
```

## Evidence

- `frontend/web/src/ui/form/useFormReset.ts:23-50` handles microtask dispatch
  and element resolution.
- `frontend/web/src/ui/form/formReset.test.ts:21-53` tests controlled input reset
  and `v-model` synchronization.
- `frontend/web/src/ui/form/formReset.test.ts:121-149` tests uncontrolled
  checkbox reset with `FormData`.
- `frontend/web/src/ui/form/formReset.test.ts:291-416` tests clearing controlled
  state to `undefined` across Checkbox, Switch, RadioGroup, and Select.

## What this does not cover

- Field-level validation and error text association: `UiField` coordinates
  `aria-invalid` and `aria-describedby` via provide/inject context, which operates
  independently of form reset cycles.
- Inputs rendered outside an HTML `<form>` element: controls without an
  ancestor form ignore reset events and rely on standard `v-model` binding updates.
