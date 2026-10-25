const { request } = require('./request')
const storage = require('./storage')

let loginPromise = null

function login() {
  return new Promise((resolve, reject) => {
    wx.login({
      success(result) {
        if (!result.code) {
          reject(new Error('微信登录未返回 code'))
          return
        }
        request({
          url: '/auth/wechat-login',
          method: 'POST',
          auth: false,
          data: { code: result.code },
        })
          .then((data) => {
            storage.setToken(data.token)
            storage.setUser(data.user)
            resolve(data.user)
          })
          .catch((error) => {
            console.error('wechat login failed', error.code || error.message)
            reject(error)
          })
      },
      fail(error) {
        console.error('wx.login failed', error)
        reject(new Error('微信登录失败，请重试'))
      },
    })
  })
}

function ensureLogin(force) {
  const token = storage.getToken()
  if (token && !force) {
    return Promise.resolve(storage.getUser())
  }
  if (force) {
    storage.clearToken()
  }
  if (!loginPromise) {
    loginPromise = login().finally(() => {
      loginPromise = null
    })
  }
  return loginPromise
}

function logout() {
  storage.clearToken()
  storage.setUser(null)
}

module.exports = {
  ensureLogin,
  logout,
}
