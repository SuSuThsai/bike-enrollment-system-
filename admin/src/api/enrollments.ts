import { buildQuery, request, requestBlob, type DownloadFile } from './http'
import type { EnrollmentListResponse, EnrollmentQuery, ExportQuery } from './types'

export function listEnrollments(params: EnrollmentQuery): Promise<EnrollmentListResponse> {
  return request<EnrollmentListResponse>(`/admin/enrollments${buildQuery({
    event_id: params.event_id,
    group_id: params.group_id,
    status: params.status,
    page: params.page,
    page_size: params.page_size,
  })}`)
}

export function exportEnrollments(params: ExportQuery): Promise<DownloadFile> {
  return requestBlob(`/admin/enrollments/export${buildQuery({
    event_id: params.event_id,
    group_id: params.group_id,
    fields: params.fields,
  })}`)
}
