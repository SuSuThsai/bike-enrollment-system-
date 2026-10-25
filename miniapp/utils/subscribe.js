const config = require('../config')
const { request } = require('./request')

function requestSubscription() {
  const templateIds = config.SUBSCRIBE_TEMPLATE_IDS || []
  if (templateIds.length === 0) {
    console.warn('SUBSCRIBE_TEMPLATE_IDS is empty; skip subscribe message request')
    return Promise.resolve([])
  }

  return new Promise((resolve) => {
    wx.requestSubscribeMessage({
      tmplIds: templateIds,
      success(result) {
        const accepted = templateIds.filter((id) => result[id] === 'accept')
        resolve(accepted)
      },
      fail(error) {
        console.warn('requestSubscribeMessage failed', error)
        resolve([])
      },
    })
  })
}

function reportSubscription(accepted) {
  if (!accepted || accepted.length === 0) {
    return Promise.resolve()
  }
  return request({
    url: '/notifications/subscribe',
    method: 'POST',
    data: { template_ids: accepted },
  }).catch((error) => {
    console.warn('report subscription failed', error.code || error.message)
  })
}

// 必须在用户点击事件链路中调用；授权/上报失败不阻断支付。
function prepareBeforePayment() {
  return requestSubscription()
    .then((accepted) => reportSubscription(accepted).then(() => accepted))
    .catch((error) => {
      console.warn('prepare subscription failed', error)
      return []
    })
}

module.exports = {
  prepareBeforePayment,
}
