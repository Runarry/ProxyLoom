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
    { path: '/sources', name: 'sources', component: () => import('./views/SourcesPage.vue') },
    { path: '/sources/new', name: 'source-new', component: () => import('./views/SourceEditorPage.vue') },
    { path: '/sources/:id', name: 'source-detail', component: () => import('./views/SourceDetailPage.vue') },
    { path: '/sources/:id/edit', name: 'source-edit', component: () => import('./views/SourceEditorPage.vue') },
    { path: '/imports', name: 'imports', component: () => import('./views/ImportsPage.vue') },
    { path: '/imports/:id', name: 'import-detail', component: () => import('./views/ImportsPage.vue') },
    ...(['chain', 'policy_group'] as const).flatMap(kind => {
      const path = kind === 'chain' ? '/chains' : '/policy-groups'
      return [
        { path, name: `${kind}-list`, component: () => import('./views/OrchestrationListPage.vue'), props: { kind } },
        { path: `${path}/new`, name: `${kind}-new`, component: () => import('./views/OrchestrationEditorPage.vue'), props: { kind } },
        { path: `${path}/:id`, name: `${kind}-detail`, component: () => import('./views/OrchestrationDetailPage.vue'), props: { kind } },
        { path: `${path}/:id/edit`, name: `${kind}-edit`, component: () => import('./views/OrchestrationEditorPage.vue'), props: { kind } },
      ]
    }),
    ...(['routing_profile', 'rule_set', 'dns_profile'] as const).flatMap(kind => {
      const path = { routing_profile: '/routing', rule_set: '/rule-sets', dns_profile: '/dns' }[kind]
      return [
        { path, name: `${kind}-list`, component: () => import('./views/OrchestrationListPage.vue'), props: { kind } },
        { path: `${path}/new`, name: `${kind}-new`, component: () => import('./views/NetworkEditorPage.vue'), props: { kind } },
        { path: `${path}/:id`, name: `${kind}-detail`, component: () => import('./views/OrchestrationDetailPage.vue'), props: { kind } },
        { path: `${path}/:id/edit`, name: `${kind}-edit`, component: () => import('./views/NetworkEditorPage.vue'), props: { kind } },
      ]
    }),
    { path: '/client-presets', name: 'client-presets', component: () => import('./views/ClientPresetsPage.vue') },
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
