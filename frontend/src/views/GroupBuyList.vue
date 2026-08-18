<template>
  <div class="placeholder-container">
    <el-result icon="info" title="拼单广场" sub-title="拼单模块开发中（后端阶段 3），敬请期待">
      <template #extra>
        <el-button type="primary" @click="$router.push('/profile')">进入个人资料</el-button>
        <el-button @click="handleLogout">退出登录</el-button>
      </template>
    </el-result>
  </div>
</template>

<script setup>
import { useRouter } from 'vue-router'
import { useUserStore } from '../stores/user'

const router = useRouter()
const userStore = useUserStore()

function handleLogout() {
  userStore.clearToken()
  router.push('/login')
}
</script>

<style scoped>
.placeholder-container {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
}
</style>
