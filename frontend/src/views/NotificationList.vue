<template>
  <div class="page-container">
    <AppHeader />
    <main class="notify-main">
      <!-- 全部/未读 Tab：本地过滤（后端 DAO 特意不做 is_read 服务端过滤——
           未读量每轮 ≤ 页大小，本地 computed 零成本；对照订单 Tab 走
           服务端 status 过滤——全量数据维度必须 SQL） -->
      <el-tabs v-model="activeTab">
        <el-tab-pane label="全部" name="all" />
        <el-tab-pane name="unread">
          <template #label>
            未读
            <el-badge v-if="unread > 0" :value="unread" class="unread-badge" />
          </template>
        </el-tab-pane>
      </el-tabs>

      <div v-loading="loading" class="notify-list">
        <el-empty v-if="!loading && displayList.length === 0" description="暂无通知" />

        <!-- 点击通知 = 懒标记已读（乐观更新）+ 多态跳转。
             已读过的再点：markRead 返回幂等 code=0，直接跳转 -->
        <el-card
          v-for="n in displayList"
          :key="n.notification_id"
          class="notify-card"
          :class="{ unread: !n.is_read }"
          shadow="hover"
          @click="handleClick(n)"
        >
          <div class="notify-row">
            <!-- 未读蓝点：视觉锚点，扫一眼定位未处理项 -->
            <span class="dot" :class="{ active: !n.is_read }"></span>
            <div class="notify-info">
              <div class="notify-title">
                <span :class="{ 'title-unread': !n.is_read }">{{ n.title }}</span>
                <el-tag :type="categoryTagType(n.category)" size="small">
                  {{ categoryText(n.category) }}
                </el-tag>
              </div>
              <div class="notify-content">{{ n.content }}</div>
              <div class="notify-time">{{ formatTime(n.created_at) }}</div>
            </div>
          </div>
        </el-card>

        <!-- 加载更多：total 为 COUNT 精确值（与订单列表同款交互） -->
        <div class="load-more" v-if="displayList.length > 0">
          <el-button v-if="hasMore" :loading="loading" @click.stop="loadMore">加载更多</el-button>
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
import { listNotifications, markNotificationRead } from '../api/notification'
import { useNotificationStore } from '../stores/notification'
import AppHeader from '../components/AppHeader.vue'

const router = useRouter()
// 未读数走全局 store：与 AppHeader 角标共享同一状态源（改一处两处同步变）
const notificationStore = useNotificationStore()
const unread = computed(() => notificationStore.unread)

const activeTab = ref('all')
const list = ref([]) // 后端当页原始数据（全部 Tab 直用，未读 Tab 本地过滤）
const total = ref(0)
const page = ref(1)
const pageSize = 10
const loading = ref(false)

// 未读 Tab 本地过滤：Tab 切换不发请求（数据已在手）
const displayList = computed(() =>
  activeTab.value === 'unread' ? list.value.filter((n) => !n.is_read) : list.value
)
// 加载更多判断基于原始 list（全部 Tab 口径）；未读 Tab 下 hasMore 仍按
// 全量 total 判断——拉新页可能带出未读项，宁可多拉一页不错过
const hasMore = computed(() => list.value.length < total.value)

// 通知细类 → 展示映射（与后端 NotificationCategory 枚举一致）
const CATEGORY_MAP = {
  succeeded: { text: '已成团', tag: 'success' },
  failed: { text: '拼单失败', tag: 'danger' },
  pending_pay: { text: '待支付', tag: 'warning' },
  paid: { text: '已支付', tag: 'success' },
  cancelled: { text: '已取消', tag: 'info' },
  completed: { text: '已完成', tag: 'success' },
  closed: { text: '超时关闭', tag: 'danger' },
}
function categoryText(c) {
  return CATEGORY_MAP[c]?.text || c
}
function categoryTagType(c) {
  return CATEGORY_MAP[c]?.tag || 'info'
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
    const data = await listNotifications({ page: page.value, page_size: pageSize })
    total.value = data.total
    // unread 走 store（响应里带着就顺手同步——与 30s 轮询对齐同一数据源）
    notificationStore.unread = data.unread
    list.value.push(...data.list)
  } catch (e) {
    // 首屏失败必须提示：用户主动进页，无反馈是断网死象
    // （3-C-2b 接入 30s 轮询后给本函数加 silent 参数：轮询失败静默）
    if (page.value === 1 && list.value.length === 0) {
      ElMessage.error(e.message || '加载通知失败')
    }
  } finally {
    loading.value = false
  }
}

function loadMore() {
  page.value++
  fetchList(false)
}

// 点击通知：总是跳转（主意图）+ 懒标记已读（副作用，纯 best-effort）。
// 跳转不依赖标记成功——标记失败回滚本地态即可，用户的权威 is_read
// 由 30s 轮询从服务端自愈。这是后端「通知投递 best-effort」的前端镜像：
// 副作用失败不拖主流程
async function handleClick(n) {
  if (!n.is_read) {
    n.is_read = true
    // 角标走 store 乐观更新（AppHeader 与本页徽章同时 -1）
    notificationStore.unread = Math.max(0, notificationStore.unread - 1)
    markNotificationRead(n.notification_id).catch(() => {
      // 标记失败回滚本地态：通知回到未读（下次点击/下轮轮询可重试）
      // 不拦跳转：点击的主意图是看详情，标记是副作用
      n.is_read = false
      notificationStore.unread++
    })
  }
  // RefID 多态跳转：order 类 → 订单详情；group_buy 类 → 拼单详情。
  // ref_id 是字符串（雪花精度防护），路径拼接直接用
  if (n.type === 'order') {
    router.push(`/order/${n.ref_id}`)
  } else {
    router.push(`/group-buy/${n.ref_id}`)
  }
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
.notify-main {
  flex: 1;
  overflow-y: auto;
  padding: 12px 24px 24px;
  max-width: 860px;
  width: 100%;
  margin: 0 auto;
  box-sizing: border-box;
}
.unread-badge {
  margin-left: 4px;
}
.notify-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-height: 200px;
}
.notify-card {
  cursor: pointer;
}
.notify-card.unread {
  background: #ecf5ff; /* 未读底色：与已读视觉分层 */
}
.notify-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-top: 6px;
  background: transparent;
  flex-shrink: 0;
}
.dot.active {
  background: #409eff; /* 未读蓝点 */
}
.notify-info {
  flex: 1;
  min-width: 0;
}
.notify-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  color: #303133;
}
.title-unread {
  font-weight: 700;
}
.notify-content {
  font-size: 13px;
  color: #606266;
  margin-top: 4px;
  line-height: 1.5;
}
.notify-time {
  font-size: 12px;
  color: #909399;
  margin-top: 4px;
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
