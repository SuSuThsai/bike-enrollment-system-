const { request, showError } = require('../../utils/request')
const auth = require('../../utils/auth')
const storage = require('../../utils/storage')
const schemaUtil = require('../../utils/schema')
const { payOrder } = require('../../utils/payment')
const subscribe = require('../../utils/subscribe')
const { formatDateTime, formatPrice } = require('../../utils/format')

Page({
  data: {
    event: null,
    group: null,
    fields: [],
    disclaimerAccepted: false,
    disclaimerVersion: '',
    loading: true,
    lockLoading: false,
    lockError: '',
    lockUntilText: '',
    hasLock: false,
    eventOpen: true,
    registrationHint: '',
    submitting: false,
    formError: '',
    draftRestored: false,
    draftSaved: false,
  },

  onLoad(options) {
    this.eventId = options.event_id || ''
    this.groupId = options.group_id || ''
    this.lockToken = ''
    this.completed = false
    if (!this.eventId || !this.groupId) {
      this.setData({ loading: false, formError: '缺少赛事或组别参数' })
      return
    }
    this.initialize()
  },

  onHide() {
    this.persistDraft()
  },

  onUnload() {
    this.persistDraft()
  },

  initialize() {
    this.setData({ loading: true })
    auth
      .ensureLogin()
      .then(() => request({ url: `/events/${this.eventId}` }))
      .then((event) => {
        const group = (event.groups || []).find((item) => item.id === this.groupId)
        if (!group) {
          throw new Error('组别不存在或已下线')
        }
        const fields = schemaUtil.normalizeFields(group.form_schema || {})
        const draft = storage.loadDraft(group.id)
        const restoredFields = draft
          ? schemaUtil.applyDraft(fields, draft.formData)
          : fields
        const registrationStatus = event.registration_status
        const eventOpen = registrationStatus === 'open'
        let registrationHint = ''
        if (registrationStatus === 'not_started') {
          registrationHint = event.registration_open_at
            ? `报名将于 ${formatDateTime(event.registration_open_at)} 开放`
            : '报名尚未开始'
        } else if (registrationStatus === 'closed') {
          registrationHint = event.registration_closed_reason
            ? `报名已关闭：${event.registration_closed_reason}`
            : '报名已截止'
        }
        this.setData({
          event,
          group: {
            ...group,
            priceText: formatPrice(group.price),
            startText: formatDateTime(group.start_time),
          },
          fields: restoredFields,
          disclaimerVersion: group.disclaimer_version || '',
          disclaimerAccepted: draft ? Boolean(draft.disclaimerAccepted) : false,
          draftRestored: Boolean(draft),
          eventOpen,
          registrationHint,
          loading: false,
        })
        if (!eventOpen) {
          this.setData({ lockError: registrationHint })
          return
        }
        return this.acquireLock()
      })
      .catch((error) => {
        this.setData({ loading: false, formError: error.message })
        showError(error)
      })
  },

  acquireLock() {
    if (this.data.lockLoading) {
      return Promise.resolve()
    }
    this.setData({ lockLoading: true, lockError: '' })
    return request({
      url: '/enrollments/lock',
      method: 'POST',
      data: { group_id: this.groupId },
    })
      .then((lock) => {
        this.lockToken = lock.lock_token
        this.setData({
          hasLock: true,
          lockUntilText: lock.expires_at
            ? `名额已锁定，请在 ${this.formatClock(lock.expires_at)} 前提交`
            : '名额已锁定，请尽快提交',
        })
      })
      .catch((error) => {
        this.lockToken = ''
        const patch = { hasLock: false, lockError: error.message }
        if (error.code === 'EVENT_NOT_OPEN') {
          patch.eventOpen = false
          patch.registrationHint = error.message
        }
        this.setData(patch)
        showError(error)
      })
      .finally(() => this.setData({ lockLoading: false }))
  },

  formatClock(value) {
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) {
      return '15 分钟内'
    }
    const pad = (number) => String(number).padStart(2, '0')
    return `${pad(date.getHours())}:${pad(date.getMinutes())}`
  },

  onInput(event) {
    const index = Number(event.currentTarget.dataset.index)
    this.setData(
      { [`fields[${index}].value`]: event.detail.value },
      () => this.persistDraft()
    )
  },

  onSelect(event) {
    const index = Number(event.currentTarget.dataset.index)
    const optionIndex = Number(event.detail.value)
    const field = this.data.fields[index]
    const option = field.options[optionIndex]
    if (!option) {
      return
    }
    this.setData({
      [`fields[${index}].value`]: option.value,
      [`fields[${index}].selectedIndex`]: optionIndex,
      [`fields[${index}].displayValue`]: option.label,
    }, () => this.persistDraft())
  },

  onRadio(event) {
    const index = Number(event.currentTarget.dataset.index)
    this.setData(
      { [`fields[${index}].value`]: event.detail.value },
      () => this.persistDraft()
    )
  },

  onCheckbox(event) {
    const index = Number(event.currentTarget.dataset.index)
    this.setData(
      { [`fields[${index}].value`]: event.detail.value },
      () => this.persistDraft()
    )
  },

  onDate(event) {
    const index = Number(event.currentTarget.dataset.index)
    this.setData(
      { [`fields[${index}].value`]: event.detail.value },
      () => this.persistDraft()
    )
  },

  onDisclaimerChange(event) {
    const accepted = event.detail.value.length > 0
    this.setData({ disclaimerAccepted: accepted }, () => this.persistDraft())
  },

  persistDraft() {
    if (this.completed || !this.data.group || this.data.fields.length === 0) {
      return
    }
    const formData = {}
    this.data.fields.forEach((field) => {
      formData[field.name] = field.value
    })
    storage.saveDraft(this.data.group.id, {
      formData,
      disclaimerAccepted: this.data.disclaimerAccepted,
    })
    this.setData({ draftSaved: true })
  },

  retryLock() {
    this.acquireLock()
  },

  validate() {
    const error = schemaUtil.validateFields(this.data.fields)
    if (error) {
      this.setData({ formError: error.message })
      wx.pageScrollTo({
        selector: `#field-${error.fieldIndex}`,
        duration: 250,
      })
      return false
    }
    if (!this.data.disclaimerAccepted) {
      this.setData({ formError: '请先阅读并同意免责协议' })
      return false
    }
    if (!this.lockToken) {
      this.setData({ formError: '名额尚未锁定，请先点击重新锁定' })
      return false
    }
    return true
  },

  submit() {
    if (this.data.submitting || this.data.loading) {
      return
    }
    this.setData({ formError: '' })
    if (!this.validate()) {
      return
    }

    this.setData({ submitting: true })
    // 在用户点击链路内统一拉起订阅授权；不阻断报名/支付。
    subscribe
      .prepareBeforePayment()
      .then(() => this.submitEnrollment())
      .catch((error) => {
        console.warn('subscribe before payment failed', error)
        this.submitEnrollment()
      })
  },

  submitEnrollment() {
    const payload = {
      lock_token: this.lockToken,
      group_id: this.groupId,
      form_data: schemaUtil.buildFormData(this.data.fields),
      disclaimer_version: this.data.disclaimerVersion,
      disclaimer_signed_at: new Date().toISOString(),
    }

    return request({
      url: '/enrollments',
      method: 'POST',
      data: payload,
    })
      .then((result) => {
        this.clearDraftState()
        return this.paySubmittedOrder(result)
      })
      .catch((error) => {
        if (error.isNetworkError) {
          this.queueOfflineSubmission(payload)
          return
        }
        if (error.code === 'LOCK_EXPIRED' || error.code === 'LOCK_INVALID') {
          this.lockToken = ''
          this.setData({ hasLock: false, lockError: error.message, formError: error.message })
        }
        if (error.code === 'EVENT_NOT_OPEN') {
          this.setData({
            eventOpen: false,
            registrationHint: error.message,
            formError: error.message,
          })
        }
        showError(error)
      })
      .finally(() => this.setData({ submitting: false }))
  },

  clearDraftState() {
    this.completed = true
    storage.clearDraft(this.groupId)
    this.setData({ draftRestored: false, draftSaved: false })
  },

  queueOfflineSubmission(payload) {
    storage.enqueuePending({
      lock_token: payload.lock_token,
      group_id: payload.group_id,
      event_id: this.eventId,
      payload,
    })
    this.lockToken = ''
    this.setData({ hasLock: false })
    this.completed = true
    storage.clearDraft(this.groupId)
    wx.showModal({
      title: '网络中断，已暂存',
      content: '报名资料已保存在本地，网络恢复后会自动补传。请勿重复提交，稍后可在“我的报名”查看。',
      showCancel: false,
      confirmText: '去我的报名',
      success: () => {
        wx.switchTab({ url: '/pages/my-enrollments/index' })
      },
    })
  },

  paySubmittedOrder(result) {
    const orderId = result.order.id
    const enrollmentId = result.enrollment.id
    return payOrder(orderId, { subscribe: false })
      .then(() => {
        wx.redirectTo({
          url: `/pages/payment-result/index?status=success&order_id=${orderId}&enrollment_id=${enrollmentId}`,
        })
      })
      .catch((error) => {
        const cancelled = error.code === 'PAY_CANCELLED'
        wx.redirectTo({
          url: `/pages/payment-result/index?status=pending&order_id=${orderId}&enrollment_id=${enrollmentId}&message=${encodeURIComponent(cancelled ? '支付已取消，可稍后继续支付' : '支付未完成，可稍后继续支付')}`,
        })
      })
  },
})
