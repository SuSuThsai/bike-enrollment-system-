export function formatPrice(value?: number | null): string {
  if (value === undefined || value === null) {
    return '待定'
  }
  return `¥${(value / 100).toFixed(2)}`
}

export function formatDuration(minutes?: number | null): string {
  if (minutes === undefined || minutes === null || minutes <= 0) {
    return '待定'
  }
  return `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分`
}

export function formatDateTime(value?: string | null): string {
  if (!value) {
    return '待定'
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

export function formatDate(value?: string | null): string {
  if (!value) {
    return '待定'
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toLocaleDateString('zh-CN', {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
  })
}

const registrationLabels: Record<string, string> = {
  not_started: '报名未开始',
  open: '报名中',
  closed: '报名结束',
}

export function statusLabel(registrationStatus: string, lifecycleStatus?: string): string {
  if (lifecycleStatus === 'finished') {
    return '已结束'
  }
  if (lifecycleStatus === 'cancelled') {
    return '已取消'
  }
  return registrationLabels[registrationStatus] || registrationStatus
}

export function statusClass(registrationStatus: string, lifecycleStatus?: string): string {
  if (lifecycleStatus === 'finished') {
    return 'bg-slate-100 text-slate-600 ring-slate-200'
  }
  if (lifecycleStatus === 'cancelled') {
    return 'bg-rose-50 text-rose-700 ring-rose-200'
  }
  switch (registrationStatus) {
    case 'open':
      return 'bg-emerald-50 text-emerald-700 ring-emerald-200'
    case 'not_started':
      return 'bg-sky-50 text-sky-700 ring-sky-200'
    case 'closed':
      return 'bg-amber-50 text-amber-700 ring-amber-200'
    default:
      return 'bg-sky-50 text-sky-700 ring-sky-200'
  }
}

export function canRegister(
  registrationStatus: string,
  groupStatus: string,
  remaining: number,
): boolean {
  return registrationStatus === 'open' && groupStatus === 'open' && remaining > 0
}

// isDeadlineSoon 判断距离报名截止是否在 0-24 小时内（用于“即将截止”高亮）。
export function isDeadlineSoon(value?: string | null): boolean {
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
