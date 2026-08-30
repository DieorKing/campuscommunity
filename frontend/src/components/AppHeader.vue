<template>
  <header class="page-header">
    <div class="brand" @click="$router.push('/group-buys')">
      <img :src="logoUrl" alt="校园拼团" class="brand-logo" />
      <span class="brand-name">校园拼团</span>
    </div>
    <div class="header-actions">
      <slot>
        <!-- 默认导航：登录态各页面通用；需要定制操作位的页面用 slot 覆盖 -->
        <el-button type="primary" @click="$router.push('/publish')">发布拼单</el-button>
        <el-button text @click="$router.push('/orders')">我的订单</el-button>
        <!-- 通知入口带未读角标：badge 由 30s 轮询 + 业务动作 bump 刷新 -->
        <el-badge :value="notificationStore.unread" :hidden="notificationStore.unread === 0" :max="99">
          <el-button text @click="$router.push('/notifications')">通知</el-button>
        </el-badge>
        <el-button text @click="$router.push('/profile')">个人资料</el-button>
        <el-button text type="danger" @click="handleLogout">退出登录</el-button>
      </slot>
    </div>
  </header>
</template>

<script setup>
import { onMounted, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { useUserStore } from '../stores/user'
import { useNotificationStore } from '../stores/notification'
import logoUrl from '../assets/logoGroupBuy.png'

const router = useRouter()
const userStore = useUserStore()
const notificationStore = useNotificationStore()

// 30s 轮询挂 AppHeader 生命周期：每个登录页恰好一个 AppHeader 实例，
// 页面切换 = 旧实例卸载（停轮询）+ 新实例挂载（起轮询并立即刷一次角标）
// ——随登录态自动启停，登出跳 /login（无 AppHeader）天然停表
onMounted(() => notificationStore.startPolling())
onBeforeUnmount(() => notificationStore.stopPolling())

function handleLogout() {
  // 登出先停轮询再跳转（虽然 onBeforeUnmount 也会兜底，显式声明意图）
  notificationStore.stopPolling()
  userStore.clearToken()
  router.push('/login')
}
</script>

<style scoped>
.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 24px;
  height: 56px;
  border-bottom: 1px solid #e4e7ed;
  background: #fff;
  flex-shrink: 0;
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  cursor: pointer;
}
.brand-logo {
  width: 32px;
  height: 32px;
  object-fit: contain;
}
.brand-name {
  font-size: 18px;
  font-weight: 600;
  color: #303133;
}
</style>
