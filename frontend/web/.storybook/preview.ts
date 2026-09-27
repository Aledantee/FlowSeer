import type { Preview } from '@storybook/vue3-vite'
import { withThemeByDataAttribute } from '@storybook/addon-themes'
import '@fontsource-variable/inter'
import '../src/theme/tailwind.css'
import '../src/style.css'
import '../src/dashboard.css'

const preview: Preview = {
  parameters: {
    a11y: {
      test: 'error',
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
  ],
}

export default preview
