<template>
  <div class="page-container">
    <AppHeader />
    <main class="profile-main">
      <el-card class="profile-card">
        <template #header>
          <div class="card-header">
            <span>个人资料</span>
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
            <el-avatar :src="userInfo.avatar || undefined" :size="64" class="avatar-preview">
              头像
            </el-avatar>
            <!-- 自定义上传：绕过 el-upload 默认 action，走 request 拦截器（带 token） -->
            <el-upload
              :show-file-list="false"
              accept=".jpg,.jpeg,.png,.webp"
              :before-upload="beforeAvatarUpload"
              :http-request="doAvatarUpload"
            >
              <el-button :loading="uploadingAvatar">上传头像</el-button>
            </el-upload>
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
    </main>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useUserStore } from '../stores/user'
import AppHeader from '../components/AppHeader.vue'

const userStore = useUserStore()

const savingProfile = ref(false)
const savingAddress = ref(false)
const uploadingAvatar = ref(false)

// 本地编辑态：从已加载的个人资料初始化
const edit = reactive({
  nickname: '',
  phone: '',
  address: '',
})

const userInfo = computed(() => userStore.userInfo)

onMounted(async () => {
  try {
    await userStore.fetchProfile()
    Object.assign(edit, {
      nickname: userInfo.value.nickname || '',
      phone: userInfo.value.phone || '',
      address: userInfo.value.address || '',
    })
  } catch (e) {
    ElMessage.error(e.message || '加载个人资料失败')
  }
})

// 头像上传前置校验：类型与大小在客户端先拦一层（后端魔数/大小校验仍是权威，
// 客户端校验只为省一次必然失败的请求）
function beforeAvatarUpload(file) {
  const okTypes = ['image/jpeg', 'image/png', 'image/webp']
  if (!okTypes.includes(file.type)) {
    ElMessage.error('仅支持 jpg/png/webp 格式')
    return false
  }
  if (file.size > 5 * 1024 * 1024) {
    ElMessage.error('头像不能超过 5MB')
    return false
  }
  return true
}

// 头像上传：自定义 http-request（el-upload 默认 action 不经过 axios 拦截器）。
// 上传即生效（后端写库），成功后 fetchProfile 刷新 userInfo，界面头像同步
async function doAvatarUpload({ file }) {
  uploadingAvatar.value = true
  try {
    await userStore.uploadAvatar(file)
    ElMessage.success('头像已更新')
  } catch (e) {
    // 后端业务码：10006 格式不支持 / 10007 文件过大（客户端校验外的兜底提示）
    ElMessage.error(e.message || '上传失败')
  } finally {
    uploadingAvatar.value = false
  }
}

// 保存资料：只提交「修改过的字段」（PATCH 部分更新，与后端接口语义一致）
async function handleSaveProfile() {
  const patch = {}
  if (edit.nickname !== (userInfo.value.nickname || '')) patch.nickname = edit.nickname
  if (edit.phone !== (userInfo.value.phone || '')) patch.phone = edit.phone

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
</script>

<style scoped>
.page-container {
  height: 100%;
  display: flex;
  flex-direction: column;
  background: #f5f7fa;
}
.profile-main {
  flex: 1;
  overflow-y: auto;
  padding: 24px 16px;
}
.profile-card {
  max-width: 640px;
  margin: 0 auto;
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
