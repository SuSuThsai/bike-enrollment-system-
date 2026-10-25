const { payOrder } = require('../../utils/payment')
const { showError } = require('../../utils/request')

const RESULT_META = {
  success: {
    title: '支付成功',
    description: '报名已提交，支付结果以小程序订单记录为准。',
    icon: '✓',
    tone: 'success',
  },
  pending: {
    title: '支付未完成',
    description: '订单仍为待支付，可稍后在我的报名中继续支付。',
    icon: '!',
    tone: 'pending',
  },
  fail: {
    title: '支付失败',
    description: '本次支付未完成，请稍后重试。',
    icon: '×',
    tone: 'fail',
  },
}

Page({
  data: {
    status: 'pending',
    title: '支付未完成',
    description: '',
    icon: '!',
    tone: 'pending',
    orderId: '',
    enrollmentId: '',
    paying: false,
  },

  onLoad(options) {
    const status = options.status || 'pending'
    const meta = RESULT_META[status] || RESULT_META.pending
    this.setData({
      status,
      title: meta.title,
      description: options.message ? decodeURIComponent(options.message) : meta.description,
      icon: meta.icon,
      tone: meta.tone,
      orderId: options.order_id || '',
      enrollmentId: options.enrollment_id || '',
    })
  },

  payAgain() {
    if (!this.data.orderId || this.data.paying) {
      return
    }
    this.setData({ paying: true })
    payOrder(this.data.orderId)
      .then(() => {
        const meta = RESULT_META.success
        this.setData({
          status: 'success',
          title: meta.title,
          description: meta.description,
          icon: meta.icon,
          tone: meta.tone,
        })
        wx.showToast({ title: '支付成功', icon: 'success' })
      })
      .catch((error) => {
        if (error.code !== 'PAY_CANCELLED') {
          showError(error)
        }
      })
      .finally(() => this.setData({ paying: false }))
  },

  goMyEnrollments() {
    wx.switchTab({ url: '/pages/my-enrollments/index' })
  },

  goEvents() {
    wx.switchTab({ url: '/pages/event-list/index' })
  },
})
