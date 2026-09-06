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
    { path: '/', redirect: '/devices' },
    { path: '/:view(devices|sites|topology|components)', component: FleetView },
    { path: '/:pathMatch(.*)*', redirect: '/devices' },
  ],
})
createApp(App).use(router).mount('#app')
