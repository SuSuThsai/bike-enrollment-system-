const config = require('../config')
const storage = require('./storage')

const ERROR_MESSAGES = {
  AUTH_INVALID_TOKEN: '登录状态已失效，请重试',
  AUTH_EXPIRED: '登录已过期，请重试',
  AUTH_INVALID_CODE: '微信登录失败，请重试',
  INVALID_ARGUMENT: '请求参数有误',
  RATE_LIMITED: '你已有一笔进行中的报名，请稍后再试',
  GROUP_SOLD_OUT: '该组别名额已满',
  GROUP_CLOSED: '该组别已关闭报名',
  EVENT_NOT_OPEN: '赛事暂未开放报名',
  GROUP_NOT_FOUND: '组别不存在',
  LOCK_INVALID: '名额锁无效，请重新选择组别',
  LOCK_EXPIRED: '名额锁已过期，请重新锁定',
  LOCK_ALREADY_CONSUMED: '该名额锁已提交过报名',
  ENROLLMENT_DUPLICATE: '你已报名该组别',
  DISCLAIMER_VERSION_MISMATCH: '免责协议版本已更新，请刷新后重试',
  ORDER_NOT_FOUND: '订单不存在',
  ORDER_STATUS_INVALID: '订单状态不支持支付',
  ENROLLMENT_NOT_FOUND: '报名记录不存在',
  NOT_IMPLEMENTED: '该功能暂未开放',
  INTERNAL_ERROR: '服务繁忙，请稍后重试',
}

function request(options) {
  return new Promise((resolve, reject) => {
    const header = Object.assign(
      { 'content-type': 'application/json' },
      options.header || {}
    )
    if (options.auth !== false) {
      const token = storage.getToken()
      if (token) {
        header.Authorization = `Bearer ${token}`
      }
    }

    wx.request({
      url: `${config.API_BASE}${options.url}`,
      method: options.method || 'GET',
      data: options.data || {},
      header,
      timeout: options.timeout || 15000,
      success(response) {
        const status = response.statusCode
        if (status >= 200 && status < 300) {
          resolve(response.data)
          return
        }

        const body = response.data || {}
        // EVENT_NOT_OPEN 的 message 由后端按原因生成（手动关闭原因 / 截止时间 / 开放时间），
        // 必须优先透传，不能被本地兜底文案覆盖。
        const message = body.code === 'EVENT_NOT_OPEN'
          ? (body.message || ERROR_MESSAGES.EVENT_NOT_OPEN)
          : (ERROR_MESSAGES[body.code] || body.message || `请求失败（${status}）`)
        const error = new Error(message)
        error.code = body.code || `HTTP_${status}`
        error.status = status
        error.requestId = body.request_id
        if (status === 401) {
          storage.clearToken()
        }
        console.error('request failed', options.url, error.code, error.requestId)
        reject(error)
      },
      fail(cause) {
        const error = new Error('网络连接失败，请检查网络后重试')
        error.code = 'NETWORK_ERROR'
        error.isNetworkError = true
        error.cause = cause
        console.error('request network failed', options.url, cause)
        reject(error)
      },
    })
  })
}

function showError(error) {
  const message = error && error.message ? error.message : '操作失败，请重试'
  wx.showToast({
    title: message,
    icon: 'none',
    duration: 2600,
  })
}

module.exports = {
  request,
  showError,
  ERROR_MESSAGES,
}
