import type { Preview } from '@storybook/vue3-vite'
import { setup } from '@storybook/vue3-vite'
import { withThemeByDataAttribute } from '@storybook/addon-themes'
import { createWebI18n } from '../src/i18n'
import { withAiTargets } from './aiDecorator'
import { withLocale } from './i18nDecorator'
import '@fontsource-variable/inter'
import '../src/theme/tailwind.css'
import '../src/style.css'

setup((app) => {
  app.use(createWebI18n())
})

const preview: Preview = {
  parameters: {
    a11y: {
      test: 'error',
    },
  },
  globalTypes: {
    locale: {
      description: 'Internationalization locale',
      defaultValue: 'en',
      toolbar: {
        icon: 'globe',
        items: [
          { value: 'en', title: 'English' },
          { value: 'de', title: 'Deutsch' },
        ],
        dynamicTitle: true,
      },
    },
  },
  decorators: [
    withThemeByDataAttribute({
      themes: {
        light: 'light',
        dark: 'dark',
      },
      defaultTheme: 'light',
      attributeName: 'data-theme',
    }),
    withLocale,
    withAiTargets,
  ],
}

export default preview
