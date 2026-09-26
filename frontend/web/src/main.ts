import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import FleetView from './FleetView.vue'
import '@fontsource-variable/inter/standard.css'
import '@fontsource-variable/dm-sans'
import './style.css'
const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/dashboard' },
    {
      path: '/:view(dashboard|devices|sites|topology|components)',
      component: FleetView,
    },
    { path: '/:pathMatch(.*)*', redirect: '/dashboard' },
  ],
})
createApp(App).use(router).mount('#app')
// The dynamic import keeps React and Agentation out of production bundles.
if (import.meta.env.DEV)
  void import('./dev/agentation').then(({ mountAgentation }) =>
    mountAgentation(),
  )
