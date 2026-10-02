// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import {
  createApp,
  defineComponent,
  h,
  nextTick,
  reactive,
  type App,
  type Component,
} from 'vue'
import { composeStories, setProjectAnnotations } from '@storybook/vue3-vite'
import { useI18n } from 'vue-i18n'
import preview from './preview'
import { createWebI18n } from '../src/i18n'

setProjectAnnotations(preview)

const LocaleProbe = defineComponent({
  name: 'LocaleProbe',
  setup() {
    const { t } = useI18n()
    return () => h('div', { class: 'probe-text' }, t('ui.statusBadge.healthy'))
  },
})

const storyModule = {
  default: {
    title: 'ProbeStory',
    component: LocaleProbe,
  },
  Default: {
    render: () => h(LocaleProbe),
  },
}

let mounted: { app: App; container: HTMLElement }[] = []

function mountStory(component: Component) {
  const container = document.createElement('div')
  document.body.append(container)
  const app = createApp(component)
  app.use(createWebI18n())
  app.mount(container)
  mounted.push({ app, container })
  return { app, container }
}

afterEach(() => {
  for (const { app, container } of mounted) {
    app.unmount()
    container.remove()
  }
  mounted = []
  document.body.replaceChildren()
})

describe('i18nDecorator', () => {
  it('mounts a translated probe in both locales using composeStories with matching initialGlobals', async () => {
    const enStories = composeStories(storyModule, {
      initialGlobals: { locale: 'en' },
    })
    const deStories = composeStories(storyModule, {
      initialGlobals: { locale: 'de' },
    })

    const enMount = mountStory(enStories.Default)
    const deMount = mountStory(deStories.Default)
    await nextTick()

    expect(enMount.container.querySelector('.probe-text')?.textContent).toBe(
      'Healthy',
    )
    expect(deMount.container.querySelector('.probe-text')?.textContent).toBe(
      'Gesund',
    )
  })

  it('changes reactive(Story.globals).locale without remounting', async () => {
    const stories = composeStories(storyModule, {
      initialGlobals: { locale: 'en' },
    })
    const mountedStory = mountStory(stories.Default)
    await nextTick()

    const probe = mountedStory.container.querySelector('.probe-text')
    expect(probe?.textContent).toBe('Healthy')

    reactive(stories.Default.globals).locale = 'de'
    await nextTick()

    expect(probe?.textContent).toBe('Gesund')
  })

  it('mounts en and de canvases together and unmounting either leaves the others translations usable', async () => {
    const enStories = composeStories(storyModule, {
      initialGlobals: { locale: 'en' },
    })
    const deStories = composeStories(storyModule, {
      initialGlobals: { locale: 'de' },
    })

    const enMount = mountStory(enStories.Default)
    const deMount = mountStory(deStories.Default)
    await nextTick()

    expect(enMount.container.textContent).toContain('Healthy')
    expect(deMount.container.textContent).toContain('Gesund')

    enMount.app.unmount()
    enMount.container.remove()
    await nextTick()

    const deProbe = deMount.container.querySelector('.probe-text')
    expect(deProbe?.textContent).toBe('Gesund')

    reactive(deStories.Default.globals).locale = 'en'
    await nextTick()

    expect(deProbe?.textContent).toBe('Healthy')
  })
})
