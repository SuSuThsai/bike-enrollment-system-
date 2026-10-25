export type LifecycleStatus = 'draft' | 'published' | 'finished' | 'cancelled'
export type RegistrationStatus = 'not_started' | 'open' | 'closed'

export type GroupStatus = 'open' | 'closed' | 'sold_out'
export type EnrollmentStatus =
  | 'pending_payment'
  | 'paid'
  | 'confirmed'
  | 'cancelled'
  | 'refunded'

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

export interface Enrollment {
  id: string
  event_id: string
  group_id: string
  user_id: string
  order_id: string
  status: EnrollmentStatus
  form_data?: Record<string, unknown>
  created_at: string
}

export interface EnrollmentListResponse {
  items: Enrollment[]
  total: number
  page: number
  page_size: number
}

export interface GroupStats {
  group_id: string
  total_slots: number
  active_lock_count: number
  pending_payment_count: number
  paid_count: number
  remaining_slots: number
}

export interface StatsOverview {
  event_id: string
  groups: GroupStats[]
}

export interface CreateEventPayload {
  title: string
  cover_url?: string
  registration_open_at: string
  registration_deadline: string
  start_time: string
  end_time: string
  location: string
  description: string
  rules?: string
}

export type UpdateEventPayload = Partial<CreateEventPayload>

export interface CreateGroupPayload {
  event_id: string
  name: string
  price: number
  total_slots: number
  start_time: string
  form_schema: Record<string, unknown>
  disclaimer_version: string
}

export interface UpdateGroupPayload {
  name?: string
  price?: number
  total_slots?: number
  start_time?: string
  form_schema?: Record<string, unknown>
  disclaimer_version?: string
}

export interface EnrollmentQuery {
  event_id?: string
  group_id?: string
  status?: EnrollmentStatus | ''
  page?: number
  page_size?: number
}

export interface ExportQuery {
  event_id: string
  group_id?: string
  fields?: string
}

export interface DeleteResult {
  id: string
  deleted: boolean
}

export type RebuildMode = 'created' | 'overwritten' | 'unchanged'

export interface RebuildRemainingResult {
  group_id: string
  total_slots: number
  remaining_slots: number
  previous_remaining?: number
  mode: RebuildMode
  active_lock_count: number
  pending_payment_count: number
  paid_count: number
}

export interface LockLeakSample {
  token: string
  expires_at: string
  overdue_seconds: number
}

export interface PendingTimeoutSample {
  order_id: string
  created_at: string
  overdue_minutes: number
}

export interface GroupReleaseHealth {
  group_id: string
  group_name: string
  lock_leak_count: number
  max_overdue_seconds: number
  pending_timeout_count: number
  leak_samples: LockLeakSample[]
  pending_samples: PendingTimeoutSample[]
}

export interface ReleaseHealth {
  event_id: string
  checked_at: string
  thresholds: {
    lock_overdue_seconds: number
    pay_timeout_minutes: number
  }
  groups: GroupReleaseHealth[]
}

export interface EventRegistrationState {
  event_id: string
  lifecycle_status: LifecycleStatus
  registration_status: RegistrationStatus
  registration_manually_closed: boolean
  registration_closed_reason?: string | null
  registration_open_at?: string | null
  registration_deadline?: string | null
}
