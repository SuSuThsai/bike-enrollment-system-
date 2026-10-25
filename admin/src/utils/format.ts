export function formatDateTime(value?: string | null): string {
  if (!value) {
    return '-'
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

export function formatPrice(value?: number | null): string {
  if (value === undefined || value === null) {
    return '-'
  }
  return `¥${(value / 100).toFixed(2)}`
}

export function formatFormData(value?: Record<string, unknown> | null): string {
  if (!value || Object.keys(value).length === 0) {
    return '-'
  }
  return JSON.stringify(value)
}

export function toDate(value?: string | null): Date | null {
  if (!value) {
    return null
  }
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

export function toISOString(value: Date | null): string | undefined {
  return value ? value.toISOString() : undefined
}

// formatDuration 格式化后端返回的 duration_minutes；不在前端重复计算时长。
export function formatDuration(minutes?: number | null): string {
  if (minutes === undefined || minutes === null || minutes <= 0) {
    return '—'
  }
  return `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分`
}
