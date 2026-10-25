import { buildQuery, request } from './http'
import type { ReleaseHealth, StatsOverview } from './types'

export function getStatsOverview(eventId: string): Promise<StatsOverview> {
  return request<StatsOverview>(`/admin/stats/overview${buildQuery({ event_id: eventId })}`)
}

export function getReleaseHealth(eventId: string): Promise<ReleaseHealth> {
  return request<ReleaseHealth>(`/admin/events/${encodeURIComponent(eventId)}/release-health`)
}
