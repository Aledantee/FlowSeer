import { createApp, h, nextTick } from 'vue'
import type { Component } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'
import { createWebI18n } from '../i18n'
import type { WebLocale } from '../i18n'
import { createAiRegistry } from '../ai'
import { aiRegistryKey } from '../ui/ai/context'
import { UiAppRoot } from '../ui'
import AppFrame from './AppFrame.vue'

export async function mountInFrame(
  view: Component,
  path: string,
  routes: RouteRecordRaw[],
  locale: WebLocale = 'en',
) {
  const host = document.createElement('div')
  document.body.append(host)
  const router = createRouter({ history: createMemoryHistory(), routes })
  const registry = createAiRegistry()
  const i18n = createWebI18n(locale)
  const app = createApp({
    render: () => h(UiAppRoot, {}, () => h(AppFrame, {}, () => h(view))),
  })
  await router.push(path)
  app.use(i18n).use(router)
  app.provide(aiRegistryKey, registry)
  await router.isReady()
  app.mount(host)
  await nextTick()
  return { host, router, registry, i18n, dispose: () => app.unmount() }
}
