const { request, showError } = require('../../utils/request')
const auth = require('../../utils/auth')
const { payOrder } = require('../../utils/payment')
const { formatDateTime, statusLabel } = require('../../utils/format')

Page({
  data: {
    items: [],
    loading: true,
    error: '',
    payingOrderId: '',
    skeletons: [1, 2, 3],
  },

  onShow() {
    this.loadEnrollments()
  },

  onPullDownRefresh() {
    this.loadEnrollments()
  },

  loadEnrollments() {
    this.setData({ loading: true, error: '' })
    auth
      .ensureLogin()
      .then(() => request({ url: '/enrollments/my' }))
      .then((data) => {
        const items = (data.items || []).map((item) => ({
          ...item,
          statusText: statusLabel(item.status),
          statusTone: this.statusTone(item.status),
          createdText: formatDateTime(item.created_at),
          canPay: item.status === 'pending_payment',
        }))
        this.setData({ items })
      })
      .catch((error) => {
        this.setData({ error: error.message })
        showError(error)
      })
      .finally(() => {
        this.setData({ loading: false })
        wx.stopPullDownRefresh()
      })
  },

  statusTone(status) {
    if (status === 'paid' || status === 'confirmed') {
      return 'open'
    }
    if (status === 'pending_payment') {
      return 'closed'
    }
    if (status === 'cancelled') {
      return 'danger'
    }
    return 'muted'
  },

  payPending(event) {
    const orderId = event.currentTarget.dataset.orderId
    if (!orderId || this.data.payingOrderId) {
      return
    }
    this.setData({ payingOrderId: orderId })
    payOrder(orderId)
      .then(() => {
        wx.navigateTo({
          url: `/pages/payment-result/index?status=success&order_id=${orderId}`,
        })
      })
      .catch((error) => {
        if (error.code !== 'PAY_CANCELLED') {
          showError(error)
        }
      })
      .finally(() => this.setData({ payingOrderId: '' }))
  },

  goEvents() {
    wx.switchTab({ url: '/pages/event-list/index' })
  },

  retry() {
    this.loadEnrollments()
  },
})
