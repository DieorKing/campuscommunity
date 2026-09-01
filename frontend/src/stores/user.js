import { defineStore } from 'pinia'
import { ref } from 'vue'
import request from '../utils/request'

export const useUserStore = defineStore('user', () => {
  const token = ref(localStorage.getItem('token') || '')
  const userInfo = ref(null)

  function setToken(val) {
    token.value = val
    localStorage.setItem('token', val)
  }

  function clearToken() {
    token.value = ''
    userInfo.value = null
    localStorage.removeItem('token')
  }

  async function login(username, password) {
    const data = await request.post('/auth/login', { username, password })
    setToken(data.token)
    return data
  }

  async function register(username, password, confirmPassword) {
    await request.post('/auth/register', { username, password, confirm_password: confirmPassword })
  }

  async function fetchProfile() {
    const data = await request.get('/user/profile')
    userInfo.value = data
    return data
  }

  async function updateProfile(data) {
    await request.patch('/user/profile', data)
  }

  async function updateAddress(address) {
    await request.put('/user/address', { address })
  }

  // 上传头像：multipart/form-data 提交文件，返回 { avatar_url }（相对路径）。
  // Content-Type 必须显式指定 multipart，让浏览器自动生成 boundary 分隔符
  async function uploadAvatar(file) {
    const fd = new FormData()
    fd.append('avatar', file)
    const data = await request.post('/user/avatar', fd, {
      headers: { 'Content-Type': 'multipart/form-data' },
    })
    // 头像上传即生效（后端已写库），立即刷新本地资料，界面头像同步更新
    await fetchProfile()
    return data
  }

  return {
    token,
    userInfo,
    setToken,
    clearToken,
    login,
    register,
    fetchProfile,
    updateProfile,
    updateAddress,
    uploadAvatar,
  }
})