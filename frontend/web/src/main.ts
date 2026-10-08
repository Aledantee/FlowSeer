import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import FleetView from './FleetView.vue'
import LoginView from './LoginView.vue'
import { aiRegistry, createMockAiHandler, installFlowSeerAi } from './ai'
import { createWebI18n } from './i18n'
import { bindDocumentLang, initialLocale } from './i18n/locale'
import { CONSOLE_HOME, LOGIN_PATH, sessionRedirect } from './session/session'
import '@fontsource-variable/inter/standard.css'
import '@fontsource-variable/dm-sans'
import './theme/tailwind.css'
import './style.css'
const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: LOGIN_PATH, component: LoginView },
    {
      path: '/:view(dashboard|devices|clients|sites|topology)',
      component: FleetView,
    },
    { path: '/devices/:deviceId', component: FleetView },
    { path: '/:pathMatch(.*)*', redirect: CONSOLE_HOME },
  ],
})
router.beforeEach(sessionRedirect)
installFlowSeerAi()
// No model backend exists yet; the preview answers summaries from fixtures.
aiRegistry.onRequest(createMockAiHandler())
const i18n = createWebI18n(initialLocale())
bindDocumentLang(i18n.global)
const app = createApp(App).use(i18n).use(router)
// A resolved initial route mounts its frame without a login-to-console morph.
void router.isReady().then(() => app.mount('#app'))
