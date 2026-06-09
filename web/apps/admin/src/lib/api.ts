import axios from "axios"

const api = axios.create({
  baseURL: process.env.NEXT_PUBLIC_API_CORE_URL ?? "/api/core",
  headers: {
    "Content-Type": "application/json",
  },
})

api.interceptors.request.use((config) => {
  if (typeof window !== "undefined") {
    const token = localStorage.getItem("auth_token")
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
  }
  return config
})

export default api
