import { defineStore } from 'pinia'

import { ADMIN_TOKEN_KEY, getToken, setToken } from '@/api/http'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: getToken(),
  }),
  actions: {
    saveToken(token: string) {
      this.token = token.trim()
      setToken(this.token)
    },
    clearToken() {
      this.token = ''
      setToken('')
    },
    refresh() {
      this.token = localStorage.getItem(ADMIN_TOKEN_KEY) || ''
    },
  },
})
