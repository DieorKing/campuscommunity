// 通知 store：全局未读角标的单一事实源（前端侧）。
// 职责：①持有 unread 状态供 AppHeader 角标 / 通知页徽章共享
//      ②30s 轮询（AppHeader 生命周期驱动启停）
//      ③bump() 主动刷新（业务动作确认后的「提前刷新」锚点）
// 注意「单一事实源」的层级：服务端 notifications.is_read 才是权威，
// 本 store 的 unread 只是缓存投影——轮询定期对齐，本地乐观更新
// （点击已读）偏差由下一轮轮询自愈
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { listNotifications } from '../api/notification'

const POLL_INTERVAL = 30000 // 30s：覆盖第三方事件（成团/超时关闭/截止判定）

export const useNotificationStore = defineStore('notification', () => {
  const unread = ref(0)
  let pollTimer = null

  // 刷角标：复用 list 接口（page_size=1 只为 unread，读合并哲学——
  // 不为角标单开一个接口）。静默失败：轮询是后台心跳，弹错误是打扰，
  // 失败这一轮不刷，下一轮自愈
  async function refreshBadge() {
    try {
      const data = await listNotifications({ page: 1, page_size: 1 })
      unread.value = data.unread
    } catch (e) {
      // 静默：未登录（10003）时 request 拦截器已跳登录页，这里无需处理
    }
  }

  // 主动刷新（bump）：挂在「自己的写操作被确认」的时刻——
  // 支付/取消接口返回成功后、抢单 /status 拿到「已建单」终态时。
  // 与轮询的关系：主动是提前（秒级感知自己干的事），轮询是兜底
  // （别人替你干的事：成团是别人抢的、关闭是扫描器干的）
  function bump() {
    refreshBadge()
  }

  // 起轮询：先立即刷一次（AppHeader 每次页面切换重新挂载 →
  // 每进一个页面角标都是新的），再进 30s 循环
  function startPolling() {
    refreshBadge()
    if (pollTimer) clearInterval(pollTimer)
    pollTimer = setInterval(refreshBadge, POLL_INTERVAL)
  }

  // 停轮询：AppHeader 卸载时调用（登出跳登录页/页面切换）。
  // 双重防御：先清定时器再置 null，重复调用安全（幂等）
  function stopPolling() {
    if (pollTimer) {
      clearInterval(pollTimer)
      pollTimer = null
    }
  }

  return { unread, refreshBadge, bump, startPolling, stopPolling }
})
