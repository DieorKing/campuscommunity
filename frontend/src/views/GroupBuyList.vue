<template>
  <div class="page-container">
    <header class="page-header">
      <div class="brand">
        <img :src="logoUrl" alt="校园拼团" class="brand-logo" />
        <span class="brand-name">校园拼团</span>
      </div>
      <div class="header-actions">
        <el-button text @click="$router.push('/profile')">个人资料</el-button>
        <el-button text type="danger" @click="handleLogout">退出登录</el-button>
      </div>
    </header>
    <div class="placeholder-container">
      <el-result icon="info" title="拼单广场" sub-title="拼单模块开发中，敬请期待" />
    </div>
  </div>
</template>

<script setup>
import { useRouter } from 'vue-router'
import { useUserStore } from '../stores/user'
// 图片资源从 assets 导入：Vite 构建时哈希命名 + 压缩，比 public/ 直链更规范
import logoUrl from '../assets/logoGroupBuy.png'

const router = useRouter()
const userStore = useUserStore()

function handleLogout() {
  userStore.clearToken()
  router.push('/login')
}
</script>

<style scoped>
.page-container {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 24px;
  height: 56px;
  border-bottom: 1px solid #e4e7ed;
  background: #fff;
}

.brand {
  display: flex;
  align-items: center;
  gap: 10px;
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

.placeholder-container {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
}
</style>
