<template>
  <div class="page-container">
    <AppHeader />
    <main class="form-main">
      <el-card class="form-card">
        <el-form ref="formRef" :model="form" :rules="rules" label-width="100px">
          <el-form-item label="商品名称" prop="title">
            <el-input v-model="form.title" placeholder="如：瑞幸生椰拿铁 3 杯起拼" maxlength="100" show-word-limit />
          </el-form-item>

          <el-form-item label="单价（元）" prop="price">
            <el-input-number v-model="form.price" :min="0.01" :max="99999" :precision="2" :step="1" />
          </el-form-item>

          <el-form-item label="成团人数" required>
            <div class="members-row">
              <el-form-item prop="min_members" class="members-item" label-width="0">
                <el-input-number v-model="form.min_members" :min="1" :max="999" />
              </el-form-item>
              <span class="members-sep">至</span>
              <el-form-item prop="max_members" class="members-item" label-width="0">
                <el-input-number v-model="form.max_members" :min="1" :max="999" />
              </el-form-item>
            </div>
          </el-form-item>

          <el-form-item label="截止时间" prop="deadline">
            <el-date-picker
              v-model="form.deadline"
              type="datetime"
              placeholder="选择拼单截止时间"
              :disabled-date="(d) => d.getTime() < Date.now() - 3600000"
            />
          </el-form-item>

          <el-form-item label="商品描述" prop="description">
            <el-input
              v-model="form.description"
              type="textarea"
              :rows="4"
              placeholder="描述商品、拼单规则等（选填）"
              maxlength="2000"
              show-word-limit
            />
          </el-form-item>

          <el-form-item label="图片链接" prop="image_url">
            <el-input v-model="form.image_url" placeholder="商品图片 URL（选填）" maxlength="255" />
          </el-form-item>

          <el-form-item>
            <el-button type="primary" :loading="submitting" @click="handleSubmit">发布</el-button>
            <el-button @click="$router.back()">取消</el-button>
          </el-form-item>
        </el-form>
      </el-card>
    </main>
  </div>
</template>

<script setup>
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { createGroupBuy } from '../api/groupbuy'
import AppHeader from '../components/AppHeader.vue'

const router = useRouter()
const formRef = ref(null)
const submitting = ref(false)

const form = reactive({
  title: '',
  price: 10,
  min_members: 2,
  max_members: 5,
  deadline: null,
  description: '',
  image_url: '',
})

const rules = {
  title: [{ required: true, message: '请输入商品名称', trigger: 'blur' }],
  price: [{ required: true, message: '请输入单价', trigger: 'blur' }],
  min_members: [
    {
      validator: (rule, value, callback) => {
        if (!value || value < 1) {
          callback(new Error('最低成团人数 ≥ 1'))
        } else if (value > form.max_members) {
          callback(new Error('最低人数不能大于上限'))
        } else {
          callback()
        }
      },
      trigger: 'blur',
    },
  ],
  max_members: [
    {
      validator: (rule, value, callback) => {
        if (!value || value < 1) {
          callback(new Error('成团上限 ≥ 1'))
        } else if (value < form.min_members) {
          callback(new Error('上限不能小于最低成团人数'))
        } else {
          callback()
        }
      },
      trigger: 'blur',
    },
  ],
  deadline: [{ required: true, message: '请选择截止时间', trigger: 'change' }],
}

async function handleSubmit() {
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return

  submitting.value = true
  try {
    // deadline 转 RFC3339：后端 time.Time 绑定要求此格式
    const data = await createGroupBuy({
      ...form,
      deadline: form.deadline.toISOString(),
    })
    ElMessage.success('发布成功')
    router.push(`/group-buy/${data.good_id}`)
  } catch (e) {
    // 20002 已截止 / 20003 参数不合法等业务码由拦截器翻译成 msg
    ElMessage.error(e.message || '发布失败')
  } finally {
    submitting.value = false
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
.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 24px;
  height: 56px;
  border-bottom: 1px solid #e4e7ed;
  background: #fff;
}
.page-title {
  font-size: 16px;
  font-weight: 600;
  color: #303133;
}
.header-placeholder {
  width: 60px;
}
.form-main {
  flex: 1;
  overflow-y: auto;
  padding: 24px;
}
.form-card {
  max-width: 640px;
  margin: 0 auto;
}
.members-row {
  display: flex;
  align-items: center;
  gap: 12px;
}
.members-item {
  margin-bottom: 0;
}
.members-sep {
  color: #909399;
}
</style>
