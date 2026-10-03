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
import UiAppRoot from '../src/ui/app/UiAppRoot.vue'
import * as appRootStoryModule from '../src/ui/app/UiAppRoot.stories'
import * as buttonStoryModule from '../src/ui/button/UiButton.stories'

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
    // First pair: unmount en, verify de continues translating and reacts to locale change
    const enStories1 = composeStories(storyModule, {
      initialGlobals: { locale: 'en' },
    })
    const deStories1 = composeStories(storyModule, {
      initialGlobals: { locale: 'de' },
    })

    const enMount1 = mountStory(enStories1.Default)
    const deMount1 = mountStory(deStories1.Default)
    await nextTick()

    expect(enMount1.container.textContent).toContain('Healthy')
    expect(deMount1.container.textContent).toContain('Gesund')

    enMount1.app.unmount()
    enMount1.container.remove()
    await nextTick()

    const deProbe1 = deMount1.container.querySelector('.probe-text')
    expect(deProbe1?.textContent).toBe('Gesund')

    reactive(deStories1.Default.globals).locale = 'en'
    await nextTick()

    expect(deProbe1?.textContent).toBe('Healthy')

    // Second pair: unmount de, verify en continues translating and reacts to locale change
    const enStories2 = composeStories(storyModule, {
      initialGlobals: { locale: 'en' },
    })
    const deStories2 = composeStories(storyModule, {
      initialGlobals: { locale: 'de' },
    })

    const enMount2 = mountStory(enStories2.Default)
    const deMount2 = mountStory(deStories2.Default)
    await nextTick()

    expect(enMount2.container.textContent).toContain('Healthy')
    expect(deMount2.container.textContent).toContain('Gesund')

    deMount2.app.unmount()
    deMount2.container.remove()
    await nextTick()

    const enProbe2 = enMount2.container.querySelector('.probe-text')
    expect(enProbe2?.textContent).toBe('Healthy')

    reactive(enStories2.Default.globals).locale = 'de'
    await nextTick()

    expect(enProbe2?.textContent).toBe('Gesund')
  })

  it('exempts UiAppRoot from being wrapped in a second UiAppRoot while wrapping other stories', async () => {
    const appRootStories = composeStories(appRootStoryModule)
    const buttonStories = composeStories(buttonStoryModule)

    const mountAppRoot = mountStory(appRootStories.Default)
    const mountButton = mountStory(buttonStories.Primary)
    await nextTick()

    const countUiAppRootAncestors = (el: Element | null): number => {
      let count = 0
      let cur = (
        el as unknown as {
          __vueParentComponent?: {
            type?: Component
            parent?: unknown
          }
        }
      )?.__vueParentComponent
      while (cur) {
        if (cur.type === UiAppRoot) {
          count++
        }
        cur = (cur as { parent?: unknown }).parent as typeof cur
      }
      return count
    }

    const appRootBtn = mountAppRoot.container.querySelector('button')
    const buttonBtn = mountButton.container.querySelector('button')

    expect(countUiAppRootAncestors(appRootBtn)).toBe(1)
    expect(countUiAppRootAncestors(buttonBtn)).toBe(1)
  })
})
