import { createRouter, createWebHistory } from 'vue-router'
import StartPage from './views/StartPage.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'start', component: StartPage },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})
