import { createRouter, createWebHistory } from 'vue-router'
import { useUserStore } from '../stores/user'

const routes = [
  {
    path: '/login',
    name: 'Login',
    component: () => import('../views/Login.vue'),
    meta: { guest: true }, // 未登录可访问
  },
  {
    path: '/register',
    name: 'Register',
    component: () => import('../views/Register.vue'),
    meta: { guest: true },
  },
  {
    path: '/group-buys',
    name: 'GroupBuyList',
    component: () => import('../views/GroupBuyList.vue'),
    meta: { requiresAuth: true }, // 首页（默认路由）；阶段3用真实拼单广场页面替换
  },
  {
    path: '/profile',
    name: 'Profile',
    component: () => import('../views/Profile.vue'),
    meta: { requiresAuth: true }, // 需登录
  },
  {
    path: '/',
    redirect: '/group-buys',
  },
  {
    path: '/:pathMatch(.*)*',
    redirect: '/group-buys',
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

// 路由守卫：未登录用户跳转登录页
router.beforeEach((to, from, next) => {
  const store = useUserStore()
  if (to.meta.requiresAuth && !store.token) {
    next('/login')
  } else if (to.meta.guest && store.token) {
    next('/group-buys')
  } else {
    next()
  }
})

export default router