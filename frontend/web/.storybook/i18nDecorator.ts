import type { Decorator } from '@storybook/vue3-vite'
import { reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import UiAppRoot from '../src/ui/app/UiAppRoot.vue'

export const withLocale: Decorator = (story, context) => {
  const isAppRoot = context.component === UiAppRoot
  return {
    name: 'WithLocale',
    components: { story, UiAppRoot },
    setup() {
      const i18n = useI18n({ useScope: 'global' })
      const globals = reactive(context.globals ?? {})
      watch(
        () => globals.locale,
        (nextLocale) => {
          if (nextLocale && (nextLocale === 'en' || nextLocale === 'de')) {
            i18n.locale.value = nextLocale
          }
        },
        { immediate: true },
      )
      return { isAppRoot }
    },
    template: `
      <story v-if="isAppRoot" />
      <UiAppRoot v-else>
        <story />
      </UiAppRoot>
    `,
  }
}
