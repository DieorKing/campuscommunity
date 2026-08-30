<template>
  <div class="page-container">
    <AppHeader />
    <main class="list-main">
      <!-- 排序切换：latest 按创建时间 / hot 按热度分 -->
      <div class="sort-bar">
        <el-radio-group v-model="sort" @change="handleSortChange">
          <el-radio-button value="latest">最新</el-radio-button>
          <el-radio-button value="hot">热门</el-radio-button>
        </el-radio-group>
        <span class="total-tip" v-if="total > 0">共 {{ total }} 个拼单</span>
      </div>

      <div v-loading="loading" class="card-list">
        <el-empty v-if="!loading && list.length === 0" description="暂无拼单，点右上角发布第一个吧" />

        <el-card
          v-for="item in list"
          :key="item.good_id"
          class="group-card"
          shadow="hover"
          @click="goDetail(item.good_id)"
        >
          <div class="card-body">
            <div class="card-left">
              <div class="card-title-row">
                <span class="card-title">{{ item.title }}</span>
                <el-tag :type="statusTagType(item.status)" size="small">{{ statusText(item.status) }}</el-tag>
              </div>
              <div class="card-progress">
                <el-progress
                  :percentage="progressPercent(item)"
                  :stroke-width="10"
                  :color="progressColor"
                />
                <span class="progress-text">{{ item.current_members }}/{{ item.max_members }} 人</span>
              </div>
              <div class="card-meta">
                <span class="card-price">¥{{ item.price.toFixed(2) }}</span>
                <span v-if="sort === 'hot' && item.hot_score > 0" class="hot-score">热度 {{ Math.round(item.hot_score) }}</span>
                <span class="card-deadline">{{ deadlineText(item.deadline) }}</span>
              </div>
            </div>
            <div class="card-right">
              <el-tag v-if="item.is_joined" type="success" effect="plain" size="small">已参与</el-tag>
              <el-button v-else type="primary" size="small" round @click.stop="goDetail(item.good_id)">
                去拼单
              </el-button>
            </div>
          </div>
        </el-card>

        <!-- 加载更多：hot 榜 total 是 ZCARD 近似值，取不到更多数据时自然终止 -->
        <div class="load-more" v-if="list.length > 0">
          <el-button v-if="hasMore" :loading="loading" @click="loadMore">加载更多</el-button>
          <span v-else class="no-more">没有更多了</span>
        </div>
      </div>
    </main>
  </div>
</template>

<script setup>
import { onMounted, onUnmounted, ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { listGroupBuy } from '../api/groupbuy'
import AppHeader from '../components/AppHeader.vue'

const router = useRouter()

const sort = ref('latest')
const list = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 10
const loading = ref(false)

const hasMore = computed(() => list.value.length < total.value)

// 拼单状态 → 展示文案 / 标签色
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
function progressPercent(item) {
  // 进度按 max 归一（凑人上限），成团线 min 由后端判定，前端不重复计算
  return Math.min(100, Math.round((item.current_members / item.max_members) * 100))
}
function deadlineText(dl) {
  const d = new Date(dl)
  const diff = d.getTime() - Date.now()
  if (diff <= 0) return '已截止'
  const hours = Math.floor(diff / 3600000)
  if (hours < 24) return `${hours} 小时后截止`
  return `${d.getMonth() + 1}/${d.getDate()} 截止`
}
const progressColor = [
  { color: '#f56c6c', percentage: 50 },
  { color: '#e6a23c', percentage: 80 },
  { color: '#67c23a', percentage: 100 },
]

async function fetchList(reset) {
  loading.value = true
  try {
    if (reset) {
      page.value = 1
      list.value = []
    }
    const data = await listGroupBuy({ sort: sort.value, page: page.value, page_size: pageSize })
    total.value = data.total
    // 追加而非替换（加载更多场景）；reset 时 list 已清空，追加等价于赋值
    list.value.push(...data.list)
  } catch (e) {
    ElMessage.error(e.message || '加载拼单列表失败')
  } finally {
    loading.value = false
  }
}

function loadMore() {
  page.value++
  fetchList(false)
}

function handleSortChange() {
  fetchList(true)
}

function goDetail(goodId) {
  router.push(`/group-buy/${goodId}`)
}

onMounted(() => fetchList(true))
// 组件卸载钩子保留位：本页无轮询/定时器，列表一次性拉取
onUnmounted(() => {})
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
.list-main {
  flex: 1;
  overflow-y: auto;
  padding: 20px 24px;
  max-width: 960px;
  width: 100%;
  margin: 0 auto;
  box-sizing: border-box;
}
.sort-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}
.total-tip {
  font-size: 13px;
  color: #909399;
}
.card-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-height: 200px;
}
.group-card {
  cursor: pointer;
}
.card-body {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
}
.card-left {
  flex: 1;
  min-width: 0;
}
.card-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.card-title {
  font-size: 15px;
  font-weight: 600;
  color: #303133;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.card-progress {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 6px;
}
.card-progress :deep(.el-progress) {
  flex: 1;
}
.progress-text {
  font-size: 13px;
  color: #606266;
  white-space: nowrap;
}
.card-meta {
  display: flex;
  align-items: baseline;
  gap: 12px;
}
.card-price {
  font-size: 16px;
  font-weight: 600;
  color: #f56c6c;
}
.hot-score {
  font-size: 12px;
  color: #e6a23c;
}
.card-deadline {
  font-size: 12px;
  color: #909399;
}
.card-right {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 6px;
  flex-shrink: 0;
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
