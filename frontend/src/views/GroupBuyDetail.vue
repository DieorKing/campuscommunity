<template>
  <div class="page-container">
    <AppHeader />
    <main class="detail-main" v-loading="loading && !detail">
      <el-empty v-if="!loading && !detail" description="拼单不存在或已被删除" />

      <template v-if="detail">
        <el-card class="info-card">
          <div class="info-header">
            <div class="info-title-row">
              <h3 class="info-title">{{ detail.title }}</h3>
              <el-tag :type="statusTagType(detail.status)" size="large">{{ statusText(detail.status) }}</el-tag>
            </div>
            <div class="price-box">
              <span class="price">¥{{ detail.price.toFixed(2) }}</span>
              <span class="price-unit">/ 份</span>
            </div>
          </div>

          <el-divider />

          <div class="progress-section">
            <div class="progress-row">
              <span>成团进度：{{ detail.current_members }} / {{ detail.max_members }} 人</span>
              <span class="min-tip">（满 {{ detail.min_members }} 人成团）</span>
            </div>
            <el-progress
              :percentage="progressPercent"
              :stroke-width="14"
              :color="progressColor"
            />
            <div class="deadline-row">
              <span>截止时间：{{ formatTime(detail.deadline) }}</span>
            </div>
          </div>

          <div v-if="detail.description" class="desc-section">
            <div class="section-title">商品描述</div>
            <p class="desc-text">{{ detail.description }}</p>
          </div>

          <div class="members-section">
            <div class="section-title">参与人员（{{ detail.members.length }}）</div>
            <div class="member-list">
              <div v-for="m in detail.members" :key="m.member_id" class="member-item">
                <el-avatar :size="32" :src="m.avatar || undefined">
                  {{ (m.nickname || '用户')[0] }}
                </el-avatar>
                <span class="member-name">{{ m.nickname || m.username || '用户' }}</span>
                <el-tag v-if="m.user_id === detail.publisher_id" size="small" type="warning">团长</el-tag>
              </div>
              <div v-for="n in Math.max(0, detail.max_members - detail.members.length)" :key="'empty' + n" class="member-item empty-slot">
                <el-avatar :size="32">+</el-avatar>
                <span class="member-name">空位</span>
              </div>
            </div>
          </div>
        </el-card>

        <!-- 操作区：按「拼单状态 × 本人参与状态」双维度渲染 -->
        <el-card class="action-card">
          <div class="action-area">
            <!-- 本人视角三态 -->
            <template v-if="detail.is_publisher">
              <el-button type="info" plain disabled>这是我发布的拼单</el-button>
            </template>
            <template v-else-if="detail.is_joined">
              <el-button type="success" plain @click="goMyOrder">查看我的订单</el-button>
            </template>
            <template v-else>
              <el-button
                v-if="canGrab"
                type="primary"
                size="large"
                :loading="grabbing"
                @click="handleGrab"
              >
                立即拼单
              </el-button>
              <el-button v-else type="info" plain disabled>{{ grabDisabledReason }}</el-button>
            </template>
          </div>
        </el-card>
      </template>
    </main>

    <!-- 抢单轮询弹窗：异步建单的「受理中 → 建单完成」用户感知层 -->
    <el-dialog
      v-model="pollDialogVisible"
      :show-close="false"
      :close-on-click-modal="false"
      :close-on-press-escape="false"
      width="360px"
      center
    >
      <div class="poll-dialog-body">
        <template v-if="polling">
          <el-icon class="is-loading" :size="42" color="#409eff"><Loading /></el-icon>
          <p class="poll-title">正在为你锁定名额…</p>
          <p class="poll-sub">订单生成中，通常只需几秒</p>
        </template>
        <template v-else-if="pollSuccess">
          <el-icon :size="42" color="#67c23a"><SuccessFilled /></el-icon>
          <p class="poll-title">拼单成功！</p>
          <el-button type="primary" @click="goPayPage">去支付</el-button>
        </template>
        <template v-else>
          <el-icon :size="42" color="#f56c6c"><CircleCloseFilled /></el-icon>
          <p class="poll-title">名额请求未成功</p>
          <p class="poll-sub">{{ pollFailMsg }}</p>
          <el-button @click="pollDialogVisible = false">知道了</el-button>
        </template>
      </div>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Loading, SuccessFilled, CircleCloseFilled } from '@element-plus/icons-vue'
import { getGroupBuyDetail, getGrabStatus, grabGroupBuy } from '../api/groupbuy'
import { useNotificationStore } from '../stores/notification'
import AppHeader from '../components/AppHeader.vue'

const route = useRoute()
// 通知角标主动刷新锚点：/status 轮询拿到「已建单」终态时 bump
const notificationStore = useNotificationStore()
const router = useRouter()

// ID 保持字符串透传（后端将 17 位雪花 ID 序列化为 string 防 JS Number 精度丢失，
// 前端任何 Number() 转换都会让 83133369331879936 变成 83133369331879940）
const goodId = route.params.id
const detail = ref(null)
const loading = ref(true)

// ---- 详情展示映射 ----
const STATUS_MAP = {
  recruiting: { text: '拼单中', tag: 'primary' },
  full: { text: '已满员', tag: 'warning' },
  succeeded: { text: '已成团', tag: 'success' },
  failed: { text: '未成团', tag: 'info' },
  expired: { text: '已截止', tag: 'info' },
}
function statusText(s) {
  return STATUS_MAP[s]?.text || s
}
function statusTagType(s) {
  return STATUS_MAP[s]?.tag || 'info'
}
function formatTime(t) {
  return new Date(t).toLocaleString('zh-CN', { hour12: false })
}
const progressPercent = computed(() => {
  if (!detail.value) return 0
  return Math.min(100, Math.round((detail.value.current_members / detail.value.max_members) * 100))
})
const progressColor = [
  { color: '#f56c6c', percentage: 50 },
  { color: '#e6a23c', percentage: 80 },
  { color: '#67c23a', percentage: 100 },
]

// ---- 抢单条件 ----
// 仅 recruiting 可抢：满员(full)/成团(succeeded)/终态(failed/expired)一律不可
const canGrab = computed(() => detail.value?.status === 'recruiting')
const grabDisabledReason = computed(() => {
  const s = detail.value?.status
  if (s === 'full') return '名额已满'
  if (s === 'succeeded') return '已成团，无法加入'
  if (s === 'failed') return '拼单未成团'
  if (s === 'expired') return '拼单已截止'
  return '暂不可拼'
})

// ---- 详情加载 ----
async function fetchDetail() {
  loading.value = true
  try {
    detail.value = await getGroupBuyDetail(goodId)
  } catch (e) {
    // 20001 拼单不存在 → 空态展示；其他（网络）也走空态
    detail.value = null
  } finally {
    loading.value = false
  }
}

// ---- 抢单 + 轮询（异步建单的前端状态机） ----
const grabbing = ref(false)
const pollDialogVisible = ref(false)
const polling = ref(false)
const pollSuccess = ref(false)
const pollFailMsg = ref('')
let pollTimer = null
const POLL_INTERVAL = 5000 // 5s 轮询
const POLL_MAX_ROUNDS = 24 // 上限 2 分钟：防消费者故障时无限转圈

function stopPolling() {
  polling.value = false
  if (pollTimer) {
    clearTimeout(pollTimer)
    pollTimer = null
  }
}

async function handleGrab() {
  grabbing.value = true
  try {
    // 受理中：grabbed=true, order_id=0（异步建单，订单号靠轮询拿）
    await grabGroupBuy(goodId)
    // 打开轮询弹窗，进入「受理中」态
    pollSuccess.value = false
    pollFailMsg.value = ''
    pollDialogVisible.value = true
    polling.value = true
    pollGrabStatus(1)
  } catch (e) {
    // 业务码已由拦截器翻译：20004 售罄 / 20005 重复 / 20006 发布者 / 20002 截止 / 20007 繁忙
    ElMessage.error(e.message || '抢单失败')
    fetchDetail() // 状态可能已变（如刚好满员），刷新详情
  } finally {
    grabbing.value = false
  }
}

function pollGrabStatus(round) {
  if (round > POLL_MAX_ROUNDS) {
    // 超时兜底：不判定失败（后端可能仍在处理），提示稍后在订单列表查看
    stopPolling()
    pollFailMsg.value = '处理时间较长，请稍后在「我的订单」查看结果'
    return
  }
  pollTimer = setTimeout(async () => {
    try {
      const st = await getGrabStatus(goodId)
      // order_id 是字符串（雪花 ID 防 JS 精度丢失）；受理中为 "0"
      if (st.grabbed && st.order_id !== '0' && st.order_id !== 0) {
        // 终态：建单完成 → 展示成功 + 去支付入口
        stopPolling()
        pollSuccess.value = true
        pollSuccessOrderId = st.order_id
        // 主动刷新通知角标：建单成功 → 待支付通知刚落库（此刻是
        // 「结果确认时刻」，比 30s 轮询提前感知）
        notificationStore.bump()
        fetchDetail() // 刷新进度与参与人员
      } else if (st.grabbed) {
        // 受理中：继续下一轮
        pollGrabStatus(round + 1)
      } else {
        // grabbed=false：理论上不可达（受理成功过才会轮询），防御性终止
        stopPolling()
        pollFailMsg.value = '抢单状态异常，请稍后在「我的订单」确认'
      }
    } catch (e) {
      // 轮询请求失败（网络抖动）不打断轮询，下轮重试
      pollGrabStatus(round + 1)
    }
  }, POLL_INTERVAL)
}

let pollSuccessOrderId = 0

function goPayPage() {
  pollDialogVisible.value = false
  router.push(`/order/${pollSuccessOrderId}`)
}

function goMyOrder() {
  // 已参与但订单号未知（如从列表页进来）：跳订单列表自行查找
  router.push('/orders')
}

onMounted(() => {
  fetchDetail()
})
// 离开页面必须停轮询：定时器随组件销毁而清除，防内存泄漏与后台空转
onUnmounted(() => {
  stopPolling()
})
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
  width: 90px;
}
.detail-main {
  flex: 1;
  overflow-y: auto;
  padding: 24px;
}
.info-card,
.action-card {
  max-width: 720px;
  margin: 0 auto 16px;
}
.info-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
}
.info-title-row {
  display: flex;
  align-items: center;
  gap: 12px;
}
.info-title {
  margin: 0;
  font-size: 20px;
  color: #303133;
}
.price-box {
  text-align: right;
}
.price {
  font-size: 26px;
  font-weight: 700;
  color: #f56c6c;
}
.price-unit {
  font-size: 13px;
  color: #909399;
}
.progress-section {
  margin-bottom: 8px;
}
.progress-row {
  font-size: 14px;
  color: #606266;
  margin-bottom: 10px;
}
.min-tip {
  font-size: 13px;
  color: #909399;
}
.deadline-row {
  margin-top: 10px;
  font-size: 13px;
  color: #909399;
}
.desc-section {
  margin-top: 8px;
}
.section-title {
  font-size: 14px;
  font-weight: 600;
  color: #303133;
  margin-bottom: 10px;
}
.desc-text {
  margin: 0;
  font-size: 14px;
  color: #606266;
  line-height: 1.7;
  white-space: pre-wrap;
}
.members-section {
  margin-top: 20px;
}
.member-list {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
}
.member-item {
  display: flex;
  align-items: center;
  gap: 8px;
}
.member-item.empty-slot {
  opacity: 0.45;
}
.member-name {
  font-size: 13px;
  color: #606266;
}
.action-card :deep(.el-card__body) {
  text-align: center;
}
.action-area {
  padding: 8px 0;
}
.poll-dialog-body {
  text-align: center;
  padding: 12px 0;
}
.poll-title {
  margin: 14px 0 6px;
  font-size: 16px;
  font-weight: 600;
  color: #303133;
}
.poll-sub {
  margin: 0 0 14px;
  font-size: 13px;
  color: #909399;
}
</style>
