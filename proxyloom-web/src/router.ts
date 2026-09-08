import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from './stores/auth'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/nodes' },
    { path: '/login', name: 'login', component: () => import('./views/AuthPage.vue'), meta: { public: true } },
    { path: '/setup', name: 'setup', component: () => import('./views/AuthPage.vue'), meta: { public: true } },
    { path: '/nodes', name: 'nodes', component: () => import('./views/NodesPage.vue') },
    { path: '/nodes/new', name: 'node-new', component: () => import('./views/NodeEditorPage.vue') },
    { path: '/nodes/:id', name: 'node-detail', component: () => import('./views/NodeDetailPage.vue') },
    { path: '/nodes/:id/edit', name: 'node-edit', component: () => import('./views/NodeEditorPage.vue') },
    { path: '/imports', name: 'imports', component: () => import('./views/ImportsPage.vue') },
    { path: '/imports/:id', name: 'import-detail', component: () => import('./views/ImportsPage.vue') },
    { path: '/:pathMatch(.*)*', name: 'not-found', component: () => import('./views/NotFoundPage.vue'), meta: { public: true } },
  ],
  scrollBehavior: () => ({ top: 0 }),
})

router.beforeEach(async to => {
  const auth = useAuthStore()
  await auth.restore()
  if (!to.meta.public && !auth.user) return { name: 'login', query: { next: to.fullPath } }
  if ((to.name === 'login' || to.name === 'setup') && auth.user) return { name: 'nodes' }
})
