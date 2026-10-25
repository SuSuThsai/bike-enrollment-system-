const STATUS_LABELS = {
  pending_payment: '待支付',
  paid: '已支付',
  confirmed: '已确认',
  cancelled: '已取消',
  refunded: '已退款',
  open: '开放',
  closed: '已关闭',
  sold_out: '已售罄',
}

const REGISTRATION_LABELS = {
  not_started: '报名未开始',
  open: '报名中',
  closed: '报名结束',
}

function registrationStatusLabel(registrationStatus, lifecycleStatus) {
  if (lifecycleStatus === 'finished') {
    return '已结束'
  }
  if (lifecycleStatus === 'cancelled') {
    return '已取消'
  }
  return REGISTRATION_LABELS[registrationStatus] || registrationStatus || '-'
}

function statusLabel(status) {
  return STATUS_LABELS[status] || status || '-'
}

function formatPrice(cents) {
  if (cents === undefined || cents === null) {
    return '待定'
  }
  return `¥${(Number(cents) / 100).toFixed(2)}`
}

function formatDateTime(value) {
  if (!value) {
    return '-'
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  const pad = (number) => String(number).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function formatDate(value) {
  if (!value) {
    return '-'
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  const pad = (number) => String(number).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

// formatDuration 格式化后端 duration_minutes；不在小程序端重复计算时长。
function formatDuration(minutes) {
  const total = Number(minutes)
  if (!Number.isFinite(total) || total <= 0) {
    return '—'
  }
  const rounded = Math.floor(total)
  return `${Math.floor(rounded / 60)} 小时 ${rounded % 60} 分`
}

// isDeadlineSoon 判断距离报名截止是否在 0-24 小时内（“即将截止”高亮）。
function isDeadlineSoon(value) {
  if (!value) {
    return false
  }
  const deadline = new Date(value).getTime()
  if (Number.isNaN(deadline)) {
    return false
  }
  const remaining = deadline - Date.now()
  return remaining > 0 && remaining < 24 * 60 * 60 * 1000
}

function remainingText(group) {
  if (!group) {
    return ''
  }
  return `${group.remaining_slots}/${group.total_slots}`
}

module.exports = {
  statusLabel,
  registrationStatusLabel,
  formatPrice,
  formatDateTime,
  formatDate,
  formatDuration,
  isDeadlineSoon,
  remainingText,
}
