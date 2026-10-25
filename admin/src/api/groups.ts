import { request } from './http'
import type {
  CreateGroupPayload,
  DeleteResult,
  Group,
  RebuildRemainingResult,
  UpdateGroupPayload,
} from './types'

export function createGroup(payload: CreateGroupPayload): Promise<Group> {
  return request<Group>('/admin/groups', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export function updateGroup(groupId: string, payload: UpdateGroupPayload): Promise<Group> {
  return request<Group>(`/admin/groups/${encodeURIComponent(groupId)}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export function deleteGroup(groupId: string): Promise<DeleteResult> {
  return request<DeleteResult>(`/admin/groups/${encodeURIComponent(groupId)}`, {
    method: 'DELETE',
  })
}

export function rebuildRemaining(
  groupId: string,
  force = false,
): Promise<RebuildRemainingResult> {
  const query = force ? '?force=true' : ''
  return request<RebuildRemainingResult>(
    `/admin/groups/${encodeURIComponent(groupId)}/rebuild-remaining${query}`,
    { method: 'POST' },
  )
}
