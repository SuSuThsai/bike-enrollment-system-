import { buildQuery, request } from './http'
import type { EventDetail, EventListResponse, RegistrationStatus } from './types'

export interface ListEventsParams {
  registration_status?: RegistrationStatus | ''
  page?: number
  page_size?: number
}

export function listEvents(params: ListEventsParams = {}): Promise<EventListResponse> {
  return request<EventListResponse>(`/events${buildQuery({
    registration_status: params.registration_status,
    page: params.page,
    page_size: params.page_size,
  })}`)
}

export function getEvent(eventId: string): Promise<EventDetail> {
  return request<EventDetail>(`/events/${encodeURIComponent(eventId)}`)
}
