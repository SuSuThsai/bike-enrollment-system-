export type LifecycleStatus = 'draft' | 'published' | 'finished' | 'cancelled'
export type RegistrationStatus = 'not_started' | 'open' | 'closed'
export type GroupStatus = 'open' | 'closed' | 'sold_out'

export interface Group {
  id: string
  event_id: string
  name: string
  price: number
  total_slots: number
  remaining_slots: number
  start_time: string
  status: GroupStatus
  form_schema?: Record<string, unknown>
  disclaimer_version?: string
}

export interface EventListItem {
  id: string
  title: string
  cover_url: string
  start_time: string
  end_time: string
  duration_minutes: number
  location: string
  lifecycle_status: LifecycleStatus
  registration_status: RegistrationStatus
  registration_closed_reason?: string | null
  registration_open_at?: string | null
  registration_deadline?: string | null
  min_price?: number
}

export interface EventDetail extends EventListItem {
  description: string
  rules?: string
  groups: Group[]
}

export interface EventListResponse {
  items: EventListItem[]
  total: number
}
