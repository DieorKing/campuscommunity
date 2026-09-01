import axios from 'axios'

const request = axios.create({
  baseURL: '/api/v1',
  timeout: 10000,
})

// 请求拦截器：自动带 token
request.interceptors.request.use(
  (config) => {
    const token = localStorage.getItem('token')
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  (error) => Promise.reject(error)
)

// 响应拦截器：统一处理业务响应码
request.interceptors.response.use(
  (response) => {
    const res = response.data
    // code !== 0 属于业务错误，直接 reject 让调用方 catch 处理
    if (res.code !== 0) {
      // token 失效（10003 需要登录 / 10004 无效Token）：清 token 跳登录页
      if (res.code === 10003 || res.code === 10004) {
        localStorage.removeItem('token')
        window.location.href = '/login'
      }
      // 业务码挂在 error 上：调用方可按 code 做针对性引导（如 20008 未填地址 → 跳个人资料）
      const err = new Error(res.msg || '请求失败')
      err.code = res.code
      return Promise.reject(err)
    }
    return res.data
  },
  (error) => {
    // HTTP 层错误（网络断开、超时、500等）
    if (error.response && error.response.status === 401) {
      localStorage.removeItem('token')
      window.location.href = '/login'
    }
    return Promise.reject(error)
  }
)

export default request