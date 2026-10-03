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

function mountStory(component: Component, configure?: (app: App) => void) {
  const container = document.createElement('div')
  document.body.append(container)
  const app = createApp(component)
  configure?.(app)
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

  it('unmounting en canvas leaves de translations usable and reactive to locale change', async () => {
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

    deMount.app.unmount()
    deMount.container.remove()
  })

  it('unmounting de canvas leaves en translations usable and reactive to locale change', async () => {
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

    deMount.app.unmount()
    deMount.container.remove()
    await nextTick()

    const enProbe = enMount.container.querySelector('.probe-text')
    expect(enProbe?.textContent).toBe('Healthy')

    reactive(enStories.Default.globals).locale = 'de'
    await nextTick()

    expect(enProbe?.textContent).toBe('Gesund')

    enMount.app.unmount()
    enMount.container.remove()
  })

  it('exempts UiAppRoot from being wrapped in a second UiAppRoot while wrapping other stories', async () => {
    let appRootCount = 0
    let buttonRootCount = 0

    const appRootStories = composeStories(appRootStoryModule)
    const buttonStories = composeStories(buttonStoryModule)

    mountStory(appRootStories.Default, (app) => {
      app.mixin({
        created() {
          if (this.$.type === UiAppRoot) {
            appRootCount += 1
          }
        },
      })
    })

    mountStory(buttonStories.Primary, (app) => {
      app.mixin({
        created() {
          if (this.$.type === UiAppRoot) {
            buttonRootCount += 1
          }
        },
      })
    })
    await nextTick()

    expect(appRootCount).toBe(1)
    expect(buttonRootCount).toBe(1)
  })
})
