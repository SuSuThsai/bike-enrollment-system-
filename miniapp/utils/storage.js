const TOKEN_KEY = 'bike_token'
const USER_KEY = 'bike_user'
const DRAFT_PREFIX = 'bike_enroll_draft_'
const PENDING_KEY = 'bike_pending_submissions'
const LAST_SYNC_KEY = 'bike_last_synced_submission'

function read(key, fallback) {
  try {
    const value = wx.getStorageSync(key)
    return value === '' || value === undefined ? fallback : value
  } catch (error) {
    console.warn('storage read failed', key, error)
    return fallback
  }
}

function write(key, value) {
  try {
    wx.setStorageSync(key, value)
  } catch (error) {
    console.warn('storage write failed', key, error)
  }
}

function remove(key) {
  try {
    wx.removeStorageSync(key)
  } catch (error) {
    console.warn('storage remove failed', key, error)
  }
}

function getToken() {
  return read(TOKEN_KEY, '') || ''
}

function setToken(token) {
  write(TOKEN_KEY, token)
}

function clearToken() {
  remove(TOKEN_KEY)
}

function getUser() {
  return read(USER_KEY, null)
}

function setUser(user) {
  write(USER_KEY, user)
}

function saveDraft(groupId, data) {
  if (!groupId) {
    return
  }
  write(DRAFT_PREFIX + groupId, {
    ...data,
    updatedAt: Date.now(),
  })
}

function loadDraft(groupId) {
  if (!groupId) {
    return null
  }
  return read(DRAFT_PREFIX + groupId, null)
}

function clearDraft(groupId) {
  if (groupId) {
    remove(DRAFT_PREFIX + groupId)
  }
}

function getPending() {
  const list = read(PENDING_KEY, [])
  return Array.isArray(list) ? list : []
}

function setPending(list) {
  write(PENDING_KEY, list)
}

function enqueuePending(item) {
  const pending = getPending()
  const exists = pending.some((entry) => entry.lock_token === item.lock_token)
  if (!exists) {
    pending.push({
      ...item,
      pending_id: `${item.group_id}_${Date.now()}`,
      queued_at: new Date().toISOString(),
    })
    setPending(pending)
  }
}

function removePending(pendingId) {
  setPending(getPending().filter((item) => item.pending_id !== pendingId))
}

function setLastSynced(result) {
  write(LAST_SYNC_KEY, result)
}

function getLastSynced() {
  return read(LAST_SYNC_KEY, null)
}

module.exports = {
  getToken,
  setToken,
  clearToken,
  getUser,
  setUser,
  saveDraft,
  loadDraft,
  clearDraft,
  getPending,
  setPending,
  enqueuePending,
  removePending,
  setLastSynced,
  getLastSynced,
}
