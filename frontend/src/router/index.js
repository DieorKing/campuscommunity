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
    meta: { requiresAuth: true }, // 首页（默认路由）：拼单广场
  },
  {
    path: '/publish',
    name: 'Publish',
    component: () => import('../views/Publish.vue'),
    meta: { requiresAuth: true }, // 发布拼单
  },
  {
    path: '/group-buy/:id',
    name: 'GroupBuyDetail',
    component: () => import('../views/GroupBuyDetail.vue'),
    meta: { requiresAuth: true }, // 拼单详情 + 抢单轮询
  },
  {
    path: '/orders',
    name: 'OrderList',
    component: () => import('../views/OrderList.vue'),
    meta: { requiresAuth: true }, // 我的订单（状态筛选 + 分页）
  },
  {
    path: '/notifications',
    name: 'NotificationList',
    component: () => import('../views/NotificationList.vue'),
    meta: { requiresAuth: true }, // 我的通知（全部/未读 Tab + 懒标记已读）
  },
  {
    path: '/order/:id',
    name: 'OrderDetail',
    component: () => import('../views/OrderDetail.vue'),
    meta: { requiresAuth: true }, // 订单详情 + 支付/取消
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