<template>
  <div class="profile-container">
    <el-card class="profile-card">
      <template #header>
        <div class="card-header">
          <span>个人资料</span>
          <el-button type="danger" plain size="small" @click="handleLogout">退出登录</el-button>
        </div>
      </template>

      <el-skeleton :rows="5" animated v-if="!userInfo" />

      <el-form v-else label-width="80px">
        <el-form-item label="用户名">
          <el-input :model-value="userInfo.username" disabled />
        </el-form-item>
        <el-form-item label="昵称">
          <el-input v-model="edit.nickname" placeholder="昵称" />
        </el-form-item>
        <el-form-item label="手机号">
          <el-input v-model="edit.phone" placeholder="手机号" />
        </el-form-item>
        <el-form-item label="头像">
          <div class="avatar-row">
            <el-input v-model="edit.avatar" placeholder="头像 URL" />
            <el-avatar :src="edit.avatar || undefined" :size="48" class="avatar-preview">
              头像
            </el-avatar>
          </div>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="savingProfile" @click="handleSaveProfile">
            保存资料
          </el-button>
        </el-form-item>

        <el-divider />

        <el-form-item label="收货地址">
          <el-input v-model="edit.address" placeholder="收货地址" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="savingAddress" @click="handleSaveAddress">
            保存地址
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useUserStore } from '../stores/user'

const router = useRouter()
const userStore = useUserStore()

const savingProfile = ref(false)
const savingAddress = ref(false)

// 本地编辑态：从已加载的个人资料初始化
const edit = reactive({
  nickname: '',
  phone: '',
  avatar: '',
  address: '',
})

const userInfo = computed(() => userStore.userInfo)

onMounted(async () => {
  try {
    await userStore.fetchProfile()
    Object.assign(edit, {
      nickname: userInfo.value.nickname || '',
      phone: userInfo.value.phone || '',
      avatar: userInfo.value.avatar || '',
      address: userInfo.value.address || '',
    })
  } catch (e) {
    ElMessage.error(e.message || '加载个人资料失败')
  }
})

// 保存资料：只提交「修改过的字段」（PATCH 部分更新，与后端接口语义一致）
async function handleSaveProfile() {
  const patch = {}
  if (edit.nickname !== (userInfo.value.nickname || '')) patch.nickname = edit.nickname
  if (edit.phone !== (userInfo.value.phone || '')) patch.phone = edit.phone
  if (edit.avatar !== (userInfo.value.avatar || '')) patch.avatar = edit.avatar

  if (Object.keys(patch).length === 0) {
    ElMessage.info('资料没有变化')
    return
  }

  savingProfile.value = true
  try {
    await userStore.updateProfile(patch)
    await userStore.fetchProfile()
    Object.assign(edit, {
      nickname: userInfo.value.nickname || '',
      phone: userInfo.value.phone || '',
      avatar: userInfo.value.avatar || '',
    })
    ElMessage.success('资料已保存')
  } catch (e) {
    ElMessage.error(e.message || '保存失败')
  } finally {
    savingProfile.value = false
  }
}

// 保存地址：单字段接口，仅在变化时提交
async function handleSaveAddress() {
  if (edit.address === (userInfo.value.address || '')) {
    ElMessage.info('地址没有变化')
    return
  }
  savingAddress.value = true
  try {
    await userStore.updateAddress(edit.address)
    await userStore.fetchProfile()
    ElMessage.success('地址已保存')
  } catch (e) {
    ElMessage.error(e.message || '保存失败')
  } finally {
    savingAddress.value = false
  }
}

function handleLogout() {
  userStore.clearToken()
  router.push('/login')
}
</script>

<style scoped>
.profile-container {
  max-width: 640px;
  margin: 40px auto;
  padding: 0 16px;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.avatar-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}

.avatar-row .el-input {
  flex: 1;
}
</style>
