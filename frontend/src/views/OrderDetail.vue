<template>
  <div class="page-container">
    <AppHeader />
    <main class="detail-main" v-loading="loading && !order">
      <el-empty v-if="!loading && !order" description="订单不存在" />

      <template v-if="order">
        <el-card class="order-card">
          <!-- 状态横幅：按订单状态机渲染 -->
          <div class="status-banner">
            <el-tag :type="statusTagType(order.status)" size="large" effect="dark">
              {{ statusText(order.status) }}
            </el-tag>
            <span v-if="order.status === 'pending_pay'" class="status-tip">
              请在 30 分钟内完成支付，超时订单将自动关闭
            </span>
            <span v-else-if="order.paid_at" class="status-tip">支付时间：{{ formatTime(order.paid_at) }}</span>
            <span v-else-if="order.closed_at" class="status-tip">关闭时间：{{ formatTime(order.closed_at) }}</span>
          </div>

          <!-- 订单信息（amount/address 为下单时刻快照） -->
          <el-descriptions :column="1" border class="order-info">
            <el-descriptions-item label="订单号">{{ order.order_id }}</el-descriptions-item>
            <el-descriptions-item label="金额">
              <span class="amount">¥{{ order.amount.toFixed(2) }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="收货地址（下单时快照）">{{ order.address || '未填写' }}</el-descriptions-item>
            <el-descriptions-item label="创建时间">{{ formatTime(order.created_at) }}</el-descriptions-item>
          </el-descriptions>
        </el-card>

        <!-- 操作区：状态机驱动的三态 UI -->
        <el-card class="pay-card" v-if="order.status === 'pending_pay'">
          <div class="section-title">支付方式</div>
          <!-- 生产环境在此接入微信/支付宝网关；MVP 模拟支付 -->
          <div class="pay-methods">
            <div class="pay-method disabled">
              <span class="pay-icon" style="color:#09bb07">💰</span>
              <span class="pay-name">微信支付</span>
              <span class="pay-tag">暂未开通</span>
            </div>
            <div class="pay-method disabled">
              <span class="pay-icon" style="color:#1677ff">🅱️</span>
              <span class="pay-name">支付宝</span>
              <span class="pay-tag">暂未开通</span>
            </div>
          </div>

          <div class="pay-actions">
            <el-button
              type="primary"
              size="large"
              :loading="paying"
              @click="handlePay"
            >
              模拟支付 ¥{{ order.amount.toFixed(2) }}
            </el-button>
            <el-button size="large" :loading="cancelling" @click="handleCancel">取消订单</el-button>
          </div>
          <p class="pay-note">演示环境：点击按钮即视为支付网关确认成功</p>
        </el-card>

        <el-card class="pay-card" v-else>
          <div class="done-area">
            <el-icon :size="40" :color="order.status === 'paid' || order.status === 'completed' ? '#67c23a' : '#909399'">
              <CircleCheckFilled v-if="order.status === 'paid' || order.status === 'completed'" />
              <CircleCloseFilled v-else />
            </el-icon>
            <p class="done-text">
              {{ order.status === 'paid' || order.status === 'completed' ? '支付完成，等待成团发货' : '订单已结束' }}
            </p>
          </div>
        </el-card>
      </template>
    </main>
  </div>
</template>

<script setup>
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { CircleCheckFilled, CircleCloseFilled } from '@element-plus/icons-vue'
import { getOrderDetail, payOrder, cancelOrder } from '../api/order'
import { useNotificationStore } from '../stores/notification'
import AppHeader from '../components/AppHeader.vue'

const route = useRoute()
// 通知角标主动刷新锚点：支付/取消确认成功的那一刻，通知刚落库（链路 <1s）
const notificationStore = useNotificationStore()

// ID 字符串透传（17 位雪花 ID，防 JS Number 精度丢失）
const orderId = route.params.id
const order = ref(null)
const loading = ref(true)
const paying = ref(false)
const cancelling = ref(false)

const STATUS_MAP = {
  pending_pay: { text: '待支付', tag: 'warning' },
  paid: { text: '已支付', tag: 'success' },
  cancelled: { text: '已取消', tag: 'info' },
  completed: { text: '已完成', tag: 'success' },
  closed: { text: '超时关闭', tag: 'danger' },
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

async function fetchDetail() {
  loading.value = true
  try {
    order.value = await getOrderDetail(orderId)
  } catch (e) {
    // 30001 不存在 / 1003 非本人 → 空态展示
    order.value = null
  } finally {
    loading.value = false
  }
}

async function handlePay() {
  paying.value = true
  try {
    await payOrder(orderId)
    ElMessage.success('支付成功')
    // 主动刷新角标：支付通知此刻刚落库（bump = 提前感知自己干的事，
    // 不等 30s 轮询）。best-effort：bump 内部静默失败，不阻塞页面流程
    notificationStore.bump()
    fetchDetail()
  } catch (e) {
    // 30002（双击第二击/状态已变）由后端状态机拒绝，刷新详情展示真实状态
    ElMessage.error(e.message || '支付失败')
    fetchDetail()
  } finally {
    paying.value = false
  }
}

async function handleCancel() {
  // 二次确认：取消是不可逆操作（名额立即释放）
  const ok = await ElMessageBox.confirm('确定取消该订单吗？名额将立即释放。', '取消订单', {
    confirmButtonText: '确定取消',
    cancelButtonText: '再想想',
    type: 'warning',
  }).catch(() => false)
  if (!ok) return

  cancelling.value = true
  try {
    await cancelOrder(orderId)
    ElMessage.success('订单已取消')
    // 同支付：取消通知刚落库，主动刷角标
    notificationStore.bump()
    fetchDetail()
  } catch (e) {
    ElMessage.error(e.message || '取消失败')
    fetchDetail()
  } finally {
    cancelling.value = false
  }
}

onMounted(() => fetchDetail())
</script>

<style scoped>
.page-container {
  height: 100%;
  display: flex;
  flex-direction: column;
  background: #f5f7fa;
}
.detail-main {
  flex: 1;
  overflow-y: auto;
  padding: 24px;
}
.order-card,
.pay-card {
  max-width: 640px;
  margin: 0 auto 16px;
}
.status-banner {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}
.status-tip {
  font-size: 13px;
  color: #909399;
}
.amount {
  font-size: 18px;
  font-weight: 700;
  color: #f56c6c;
}
.section-title {
  font-size: 14px;
  font-weight: 600;
  color: #303133;
  margin-bottom: 12px;
}
.pay-methods {
  display: flex;
  gap: 12px;
  margin-bottom: 20px;
}
.pay-method {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 16px;
  border: 1px solid #e4e7ed;
  border-radius: 8px;
  opacity: 0.55;
}
.pay-icon {
  font-size: 22px;
}
.pay-name {
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}
.pay-tag {
  margin-left: auto;
  font-size: 12px;
  color: #c0c4cc;
}
.pay-actions {
  display: flex;
  gap: 12px;
}
.pay-actions .el-button--large {
  flex: 1;
}
.pay-note {
  margin: 12px 0 0;
  font-size: 12px;
  color: #c0c4cc;
  text-align: center;
}
.done-area {
  text-align: center;
  padding: 16px 0;
}
.done-text {
  margin: 12px 0 0;
  font-size: 14px;
  color: #606266;
}
</style>
