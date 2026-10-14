import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import AppLayout from './layouts/AppLayout.vue'
import { loadSession, me } from './composables/session'
import { setUnauthorizedHandler } from './api'

export const navItems = [
  { name: 'overview', path: '/', component: () => import('./views/overview/OverviewView.vue') },
  { name: 'users', path: '/users', component: () => import('./views/users/UsersView.vue') },
  { name: 'traffic', path: '/traffic', component: () => import('./views/traffic/TrafficView.vue') },
  { name: 'nodes', path: '/nodes', component: () => import('./views/nodes/NodesView.vue') },
  { name: 'settings', path: '/settings', component: () => import('./views/settings/SettingsView.vue') },
] as const

// Pages that became tabs of the pages above. Their old addresses (and route
// names, still used for links) lead to the matching tab.
const moved: RouteRecordRaw[] = [
  { path: 'inbounds', name: 'inbounds', redirect: { name: 'nodes', query: { tab: 'inbounds' } } },
  { path: 'outbounds', name: 'outbounds', redirect: (to) => ({ name: 'nodes', query: { tab: to.query.tab === 'dns' ? 'dns' : 'outbounds' } }) },
  { path: 'connections', name: 'connections', redirect: { name: 'traffic', query: { kind: 'connections' } } },
  { path: 'access', name: 'access', redirect: { name: 'settings', query: { tab: 'access' } } },
  {
    path: 'logs',
    name: 'logs',
    redirect: (to) => ({ name: 'settings', query: to.query.tab === 'audit' ? { tab: 'logs', log: 'audit' } : { tab: 'logs' } }),
  },
]

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('./views/LoginView.vue'), meta: { public: true } },
    {
      path: '/',
      component: AppLayout,
      children: [
        ...navItems.map((item) => ({
          path: item.path === '/' ? '' : item.path.slice(1),
          name: item.name,
          component: item.component,
        })),
        ...moved,
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

let checked = false
router.beforeEach(async (to) => {
  if (!checked) {
    checked = true
    await loadSession()
  }
  if (to.meta.public) return me.value && to.name === 'login' ? { name: 'overview' } : true
  if (!me.value) return { name: 'login', query: to.fullPath === '/' ? {} : { next: to.fullPath } }
  return true
})

setUnauthorizedHandler(() => {
  me.value = null
  if (router.currentRoute.value.name !== 'login') {
    router.push({ name: 'login', query: { next: router.currentRoute.value.fullPath } })
  }
})
