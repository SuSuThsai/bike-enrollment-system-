const { request, showError } = require('../../utils/request')
const {
  formatDate,
  formatDateTime,
  formatDuration,
  formatPrice,
  isDeadlineSoon,
  registrationStatusLabel,
  statusLabel,
} = require('../../utils/format')

Page({
  data: {
    event: null,
    groups: [],
    selectedGroupId: '',
    selectedGroup: null,
    loading: true,
    error: '',
  },

  onLoad(options) {
    this.eventId = options.event_id || ''
    if (!this.eventId) {
      this.setData({ loading: false, error: '缺少赛事参数' })
      return
    }
    this.loadEvent()
  },

  // 从报名页返回时刷新，保证后台关闭报名后按钮状态及时更新。
  onShow() {
    if (this.loadedOnce) {
      this.loadEvent()
    }
  },

  loadEvent() {
    this.setData({ loading: true, error: '' })
    request({ url: `/events/${this.eventId}` })
      .then((event) => {
        const eventOpen = event.registration_status === 'open'
        const groups = (event.groups || []).map((group) => {
          const available = eventOpen && group.status === 'open' && group.remaining_slots > 0
          return {
            ...group,
            statusText: group.remaining_slots <= 0 ? '已满员' : statusLabel(group.status),
            statusTone: available ? 'open' : 'muted',
            priceText: formatPrice(group.price),
            dateText: formatDateTime(group.start_time),
            remainingText: `${group.remaining_slots}/${group.total_slots}`,
            available,
          }
        })
        const selected = groups.find((group) => group.available) || groups[0] || null
        const registrationStatus = event.registration_status
        let ctaText = '立即报名'
        let registrationHint = ''
        if (registrationStatus === 'not_started') {
          ctaText = '报名未开始'
          registrationHint = event.registration_open_at
            ? `报名开放时间：${formatDateTime(event.registration_open_at)}`
            : '报名尚未开始'
        } else if (registrationStatus === 'closed') {
          ctaText = '报名已截止'
          registrationHint = event.registration_closed_reason
            ? `报名已关闭：${event.registration_closed_reason}`
            : '报名已截止'
        }
        const boundText = (value) => (value ? formatDateTime(value) : '不限')
        const registrationWindowText = `报名：${boundText(event.registration_open_at)} 至 ${boundText(event.registration_deadline)}`
        const eventWindowText = `赛事：${formatDateTime(event.start_time)} 至 ${formatDateTime(event.end_time)}`
        this.setData({
          event: {
            ...event,
            statusText: registrationStatusLabel(event.registration_status, event.lifecycle_status),
            statusTone: event.registration_status === 'open' ? 'open' : 'muted',
            dateText: formatDate(event.start_time),
            priceText: formatPrice(event.min_price),
            eventOpen,
            ctaText,
            registrationHint,
            registrationWindowText,
            eventWindowText,
            durationText: formatDuration(event.duration_minutes),
            deadlineSoon: isDeadlineSoon(event.registration_deadline),
          },
          groups,
          selectedGroupId: selected ? selected.id : '',
          selectedGroup: selected,
        })
        this.loadedOnce = true
      })
      .catch((error) => {
        this.setData({ error: error.message })
        showError(error)
      })
      .finally(() => this.setData({ loading: false }))
  },

  selectGroup(event) {
    const id = event.currentTarget.dataset.id
    const selected = this.data.groups.find((group) => group.id === id)
    if (!selected) {
      return
    }
    this.setData({ selectedGroupId: id, selectedGroup: selected })
  },

  register() {
    if (this.data.event && this.data.event.registration_status !== 'open') {
      wx.showToast({ title: this.data.event.ctaText || '报名未开放', icon: 'none' })
      return
    }
    if (!this.data.selectedGroup || !this.data.selectedGroup.available) {
      wx.showToast({ title: '该组别暂不可报名', icon: 'none' })
      return
    }
    wx.navigateTo({
      url: `/pages/enroll-form/index?event_id=${this.eventId}&group_id=${this.data.selectedGroup.id}`,
    })
  },

  retry() {
    this.loadEvent()
  },
})
