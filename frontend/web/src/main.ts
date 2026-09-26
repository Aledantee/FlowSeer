import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import FleetView from './FleetView.vue'
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
createApp(App).use(router).mount('#app')
