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
  }
})