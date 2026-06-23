import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import AppLayout from '../components/AppLayout.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('../views/LoginView.vue'),
      meta: { guest: true },
    },
    {
      path: '/',
      component: AppLayout,
      meta: { requiresAuth: true },
      children: [
        {
          path: '',
          name: 'dashboard',
          component: () => import('../views/DashboardView.vue'),
        },
        {
          path: 'channels',
          name: 'channels',
          component: () => import('../views/ChannelsView.vue'),
        },
        {
          path: 'channels/:id',
          name: 'channel-detail',
          component: () => import('../views/ChannelDetailView.vue'),
        },
        {
          path: 'admin/global',
          name: 'admin-global',
          component: () => import('../views/AdminGlobalView.vue'),
          meta: { requiresAdmin: true },
        },
        {
          path: 'admin/users',
          name: 'admin-users',
          component: () => import('../views/AdminUsersView.vue'),
          meta: { requiresAdmin: true },
        },
        {
          path: 'admin/rbac',
          name: 'admin-rbac',
          component: () => import('../views/AdminRBACView.vue'),
          meta: { requiresAdmin: true },
        },
        {
          path: 'admin/oidc',
          name: 'admin-oidc',
          component: () => import('../views/AdminOIDCView.vue'),
          meta: { requiresAdmin: true },
        },
        {
          path: 'admin/bans',
          name: 'admin-bans',
          component: () => import('../views/AdminBansView.vue'),
          meta: { requiresAdmin: true },
        },
        {
          path: ':channel',
          redirect: to => ({ name: 'channel-detail', params: { id: to.params.channel as string } }),
        },
      ],
    },
  ],
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  await auth.checkSession()

  if (to.meta.requiresAuth && !auth.isAuthenticated) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }

  if (to.meta.guest && auth.isAuthenticated) {
    return { path: '/' }
  }

  if (to.meta.requiresAdmin && !auth.isAdmin) {
    return { path: '/' }
  }
})

export default router