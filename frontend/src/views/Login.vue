<template>
  <div class="auth-container">
    <el-card class="auth-card">
      <h2 class="auth-title">校园拼团 · 登录</h2>
      <el-form ref="formRef" :model="form" :rules="rules">
        <el-form-item prop="username">
          <el-input v-model="form.username" placeholder="用户名" size="large" />
        </el-form-item>
        <el-form-item prop="password">
          <el-input
            v-model="form.password"
            type="password"
            placeholder="密码"
            size="large"
            show-password
            @keyup.enter="handleLogin"
          />
        </el-form-item>
        <el-button
          type="primary"
          size="large"
          class="submit-btn"
          :loading="loading"
          @click="handleLogin"
        >
          登 录
        </el-button>
      </el-form>
      <div class="auth-switch">
        还没有账号？<router-link to="/register">去注册</router-link>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useUserStore } from '../stores/user'

const router = useRouter()
const userStore = useUserStore()

const formRef = ref(null)
const loading = ref(false)
const form = reactive({
  username: '',
  password: '',
})

const rules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

async function handleLogin() {
  // 表单校验不通过则直接返回（Element Plus 会给出错误提示）
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return

  loading.value = true
  try {
    await userStore.login(form.username, form.password)
    ElMessage.success('登录成功')
    router.push('/group-buys')
  } catch (e) {
    ElMessage.error(e.message || '登录失败')
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.auth-container {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
}

.auth-card {
  width: 380px;
  padding: 12px 16px;
}

.auth-title {
  text-align: center;
  margin-bottom: 24px;
  color: #303133;
}

.submit-btn {
  width: 100%;
  margin-top: 4px;
}

.auth-switch {
  margin-top: 16px;
  text-align: center;
  font-size: 14px;
  color: #909399;
}
</style>
