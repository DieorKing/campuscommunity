<template>
  <div class="page-container">
    <AppHeader />
    <main class="orders-main">
      <!-- 状态筛选 Tabs：空=全部；与后端 status query 精确匹配 -->
      <el-tabs v-model="activeStatus" @tab-change="handleTabChange">
        <el-tab-pane label="全部" name="all" />
        <el-tab-pane label="待支付" name="pending_pay" />
        <el-tab-pane label="已支付" name="paid" />
        <el-tab-pane label="已取消" name="cancelled" />
        <el-tab-pane label="超时关闭" name="closed" />
      </el-tabs>

      <div v-loading="loading" class="order-list">
        <el-empty v-if="!loading && list.length === 0" description="暂无订单" />

        <el-card
          v-for="o in list"
          :key="o.order_id"
          class="order-card"
          shadow="hover"
          @click="goDetail(o.order_id)"
        >
          <div class="order-row">
            <div class="order-info">
              <div class="order-no">订单号 {{ o.order_id }}</div>
              <div class="order-time">{{ formatTime(o.created_at) }}</div>
            </div>
            <div class="order-amount">¥{{ o.amount.toFixed(2) }}</div>
            <el-tag :type="statusTagType(o.status)">{{ statusText(o.status) }}</el-tag>
            <el-button type="primary" size="small" text>详情 ›</el-button>
          </div>
        </el-card>

        <!-- 加载更多：后端 total 为 COUNT 精确值（latest 列表同款交互） -->
        <div class="load-more" v-if="list.length > 0">
          <el-button v-if="hasMore" :loading="loading" @click="loadMore">加载更多</el-button>
          <span v-else class="no-more">没有更多了</span>
        </div>
      </div>
    </main>
  </div>
</template>

<script setup>
import { onMounted, ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { listOrders } from '../api/order'
import AppHeader from '../components/AppHeader.vue'

const router = useRouter()

const activeStatus = ref('all')
const list = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 10
const loading = ref(false)

const hasMore = computed(() => list.value.length < total.value)

// 订单状态 → 展示映射（与后端 OrderStatus 枚举一致）
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

async function fetchList(reset) {
  loading.value = true
  try {
    if (reset) {
      page.value = 1
      list.value = []
    }
    // status=all 表示不筛选（后端 status 为空 = 全部）
    const params = { page: page.value, page_size: pageSize }
    if (activeStatus.value !== 'all') {
      params.status = activeStatus.value
    }
    const data = await listOrders(params)
    total.value = data.total
    list.value.push(...data.list)
  } catch (e) {
    ElMessage.error(e.message || '加载订单失败')
  } finally {
    loading.value = false
  }
}

function loadMore() {
  page.value++
  fetchList(false)
}

function handleTabChange() {
  fetchList(true)
}

function goDetail(orderId) {
  router.push(`/order/${orderId}`)
}

onMounted(() => fetchList(true))
</script>

<style scoped>
.page-container {
  height: 100%;
  display: flex;
  flex-direction: column;
  background: #f5f7fa;
}
.orders-main {
  flex: 1;
  overflow-y: auto;
  padding: 12px 24px 24px;
  max-width: 860px;
  width: 100%;
  margin: 0 auto;
  box-sizing: border-box;
}
.order-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-height: 200px;
}
.order-card {
  cursor: pointer;
}
.order-row {
  display: flex;
  align-items: center;
  gap: 16px;
}
.order-info {
  flex: 1;
  min-width: 0;
}
.order-no {
  font-size: 14px;
  font-weight: 600;
  color: #303133;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.order-time {
  font-size: 12px;
  color: #909399;
  margin-top: 2px;
}
.order-amount {
  font-size: 16px;
  font-weight: 700;
  color: #f56c6c;
  white-space: nowrap;
}
.load-more {
  text-align: center;
  padding: 16px 0;
}
.no-more {
  font-size: 13px;
  color: #c0c4cc;
}
</style>
