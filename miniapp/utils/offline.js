const { request } = require('./request')
const storage = require('./storage')

let flushing = false

function submitPayload(payload) {
  return request({
    url: '/enrollments',
    method: 'POST',
    data: payload,
  })
}

function isNonRetryable(error) {
  if (error.isNetworkError || error.status === 401 || error.status === 429) {
    return false
  }
  return error.status >= 400 && error.status < 500
}

function flushPending() {
  if (flushing) {
    return Promise.resolve(0)
  }
  if (!storage.getToken()) {
    return Promise.resolve(0)
  }

  const pending = storage.getPending()
  if (pending.length === 0) {
    return Promise.resolve(0)
  }

  flushing = true
  const remain = []
  let synced = 0

  const run = pending.reduce((chain, item) => {
    return chain.then(() => {
      if (remain.length > 0) {
        remain.push(item)
        return null
      }
      return submitPayload(item.payload)
        .then((result) => {
          synced += 1
          storage.setLastSynced(result)
          return null
        })
        .catch((error) => {
          if (error.code === 'ENROLLMENT_DUPLICATE') {
            synced += 1
            return null
          }
          if (isNonRetryable(error)) {
            console.warn('drop non-retryable pending enrollment', error.code)
            return null
          }
          // 网络错误或 5xx 保留，等待下一次网络恢复。
          remain.push(item)
          return null
        })
    })
  }, Promise.resolve())

  return run
    .then(() => {
      storage.setPending(remain)
      if (synced > 0) {
        wx.showToast({
          title: '报名已自动补传，请到我的报名查看',
          icon: 'none',
          duration: 3000,
        })
      }
      return synced
    })
    .finally(() => {
      flushing = false
    })
}

module.exports = {
  flushPending,
  submitPayload,
}
