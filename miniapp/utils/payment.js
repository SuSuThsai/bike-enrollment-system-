const { request } = require('./request')
const subscribe = require('./subscribe')

function prepay(orderId) {
  return request({
    url: '/payments/prepay',
    method: 'POST',
    data: { order_id: orderId },
  })
}

function requestPayment(params) {
  return new Promise((resolve, reject) => {
    wx.requestPayment({
      timeStamp: params.timeStamp,
      nonceStr: params.nonceStr,
      package: params.package,
      signType: params.signType,
      paySign: params.paySign,
      success: resolve,
      fail(error) {
        const cancelled = error.errMsg && error.errMsg.indexOf('cancel') >= 0
        const paymentError = new Error(cancelled ? '支付已取消' : '支付失败，请重试')
        paymentError.code = cancelled ? 'PAY_CANCELLED' : 'PAY_FAILED'
        paymentError.cause = error
        reject(paymentError)
      },
    })
  })
}

// 支付前统一拉起订阅授权；options.subscribe=false 用于表单页已经提前授权的场景。
function payOrder(orderId, options) {
  const opts = options || {}
  const before = opts.subscribe === false
    ? Promise.resolve([])
    : subscribe.prepareBeforePayment()
  return before
    .then(() => prepay(orderId))
    .then((params) => requestPayment(params))
}

module.exports = {
  payOrder,
  prepay,
  requestPayment,
}
