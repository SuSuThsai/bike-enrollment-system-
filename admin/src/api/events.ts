import { buildQuery, request } from './http'
import type {
  CreateEventPayload,
  DeleteResult,
  EventDetail,
  EventListResponse,
  EventRegistrationState,
  RegistrationStatus,
  UpdateEventPayload,
} from './types'

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

export function createEvent(payload: CreateEventPayload): Promise<EventDetail> {
  return request<EventDetail>('/admin/events', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function updateEvent(eventId: string, payload: UpdateEventPayload): Promise<EventDetail> {
  return request<EventDetail>(`/admin/events/${encodeURIComponent(eventId)}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export function deleteEvent(eventId: string): Promise<DeleteResult> {
  return request<DeleteResult>(`/admin/events/${encodeURIComponent(eventId)}`, {
    method: 'DELETE',
  })
}

export function closeRegistration(
  eventId: string,
  reason = '',
): Promise<EventRegistrationState> {
  return request<EventRegistrationState>(
    `/admin/events/${encodeURIComponent(eventId)}/registration/close`,
    {
      method: 'PATCH',
      body: JSON.stringify({ reason }),
    },
  )
}

export function openRegistration(eventId: string): Promise<EventRegistrationState> {
  return request<EventRegistrationState>(
    `/admin/events/${encodeURIComponent(eventId)}/registration/open`,
    {
      method: 'PATCH',
    },
  )
}
