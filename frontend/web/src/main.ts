import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import FleetView from './FleetView.vue'
import {
  aiRegistry,
  createMockAiHandler,
  installFlowSeerAi,
  vAiTarget,
} from './ai'
import { createWebI18n } from './i18n'
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
createApp(App)
  .use(createWebI18n())
  .use(router)
  .directive('ai-target', vAiTarget)
  .mount('#app')
