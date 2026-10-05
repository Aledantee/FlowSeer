import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import FleetView from './FleetView.vue'
import { aiRegistry, createMockAiHandler, installFlowSeerAi } from './ai'
import { createWebI18n } from './i18n'
import { bindDocumentLang, initialLocale } from './i18n/locale'
import '@fontsource-variable/inter/standard.css'
import '@fontsource-variable/dm-sans'
import './theme/tailwind.css'
import './style.css'
const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/dashboard' },
    {
      path: '/:view(dashboard|devices|clients|sites|topology)',
      component: FleetView,
    },
    { path: '/devices/:deviceId', component: FleetView },
    { path: '/:pathMatch(.*)*', redirect: '/dashboard' },
  ],
})
installFlowSeerAi()
// No model backend exists yet; the preview answers summaries from fixtures.
aiRegistry.onRequest(createMockAiHandler())
const i18n = createWebI18n(initialLocale())
bindDocumentLang(i18n.global)
createApp(App).use(i18n).use(router).mount('#app')
