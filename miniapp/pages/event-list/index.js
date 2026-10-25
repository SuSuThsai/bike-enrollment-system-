const { request, showError } = require('../../utils/request')
const { formatDate, formatDuration, formatPrice, isDeadlineSoon, registrationStatusLabel } = require('../../utils/format')

const PAGE_SIZE = 20

Page({
  data: {
    events: [],
    loading: false,
    loadingMore: false,
    error: '',
    status: '',
    page: 1,
    total: 0,
    hasMore: false,
    skeletons: [1, 2, 3],
    filters: [
      { label: '全部', value: '' },
      { label: '报名中', value: 'open' },
      { label: '报名结束', value: 'closed' },
      { label: '报名未开始', value: 'not_started' },
    ],
  },

  onLoad() {
    this.loadEvents(true)
  },

  onPullDownRefresh() {
    this.loadEvents(true)
  },

  onReachBottom() {
    if (this.data.hasMore && !this.data.loadingMore && !this.data.loading) {
      this.loadEvents(false)
    }
  },

  selectFilter(event) {
    const status = event.currentTarget.dataset.status
    if (status === this.data.status) {
      return
    }
    this.setData({ status })
    this.loadEvents(true)
  },

  loadEvents(reset) {
    if (this.data.loading || this.data.loadingMore) {
      wx.stopPullDownRefresh()
      return
    }
    const page = reset ? 1 : this.data.page
    this.setData(reset
      ? { loading: true, error: '' }
      : { loadingMore: true })

    const query = {
      page,
      page_size: PAGE_SIZE,
    }
    // “全部”筛选时不能传 status=undefined，否则后端会按非法状态返回 400。
    if (this.data.status) {
      query.registration_status = this.data.status
    }

    request({
      url: '/events',
      data: query,
    })
      .then((data) => {
        const items = (data.items || []).map((item) => ({
          ...item,
          statusText: registrationStatusLabel(item.registration_status, item.lifecycle_status),
          statusTone: this.statusTone(item.registration_status),
          priceText: formatPrice(item.min_price),
          dateText: formatDate(item.start_time),
          deadlineText: item.registration_deadline
            ? formatDate(item.registration_deadline)
            : '-',
          deadlineSoon: isDeadlineSoon(item.registration_deadline),
          durationText: formatDuration(item.duration_minutes),
        }))
        const events = reset ? items : this.data.events.concat(items)
        this.setData({
          events,
          page,
          total: data.total || 0,
          hasMore: events.length < (data.total || 0),
          error: '',
        })
      })
      .catch((error) => {
        this.setData({ error: error.message })
        showError(error)
      })
      .finally(() => {
        this.setData({ loading: false, loadingMore: false })
        wx.stopPullDownRefresh()
      })
  },

  statusTone(status) {
    if (status === 'open') {
      return 'open'
    }
    if (status === 'closed') {
      return 'closed'
    }
    return 'muted'
  },

  openEvent(event) {
    const id = event.currentTarget.dataset.id
    wx.navigateTo({ url: `/pages/event-detail/index?event_id=${id}` })
  },

  retry() {
    this.loadEvents(true)
  },
})
