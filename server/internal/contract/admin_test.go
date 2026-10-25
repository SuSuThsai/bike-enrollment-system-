package contract

import (
	"net/http"
	"testing"
)

// TestContractAdminCreateEvent 覆盖 POST /admin/events。
func TestContractAdminCreateEvent(t *testing.T) {
	runPathContract(t, http.MethodPost, "/admin/events")
}

// TestContractAdminUpdateEvent 覆盖 PUT /admin/events/{event_id}。
func TestContractAdminUpdateEvent(t *testing.T) {
	runPathContract(t, http.MethodPut, "/admin/events/{event_id}")
}

// TestContractAdminCreateGroup 覆盖 POST /admin/groups。
func TestContractAdminCreateGroup(t *testing.T) {
	runPathContract(t, http.MethodPost, "/admin/groups")
}

// TestContractAdminUpdateGroup 覆盖 PUT /admin/groups/{group_id}。
func TestContractAdminUpdateGroup(t *testing.T) {
	runPathContract(t, http.MethodPut, "/admin/groups/{group_id}")
}

// TestContractAdminDeleteEvent 覆盖 DELETE /admin/events/{event_id}。
func TestContractAdminDeleteEvent(t *testing.T) {
	runPathContract(t, http.MethodDelete, "/admin/events/{event_id}")
}

// TestContractAdminDeleteGroup 覆盖 DELETE /admin/groups/{group_id}。
func TestContractAdminDeleteGroup(t *testing.T) {
	runPathContract(t, http.MethodDelete, "/admin/groups/{group_id}")
}

// TestContractAdminRebuildGroupRemaining 覆盖 POST /admin/groups/{group_id}/rebuild-remaining。
func TestContractAdminRebuildGroupRemaining(t *testing.T) {
	runPathContract(t, http.MethodPost, "/admin/groups/{group_id}/rebuild-remaining")
}

// TestContractAdminListEnrollments 覆盖 GET /admin/enrollments。
func TestContractAdminListEnrollments(t *testing.T) {
	runPathContract(t, http.MethodGet, "/admin/enrollments")
}

// TestContractAdminExportEnrollments 覆盖 GET /admin/enrollments/export。
func TestContractAdminExportEnrollments(t *testing.T) {
	runPathContract(t, http.MethodGet, "/admin/enrollments/export")
}

// TestContractAdminStatsOverview 覆盖 GET /admin/stats/overview。
func TestContractAdminStatsOverview(t *testing.T) {
	runPathContract(t, http.MethodGet, "/admin/stats/overview")
}

// TestContractAdminReleaseHealth 覆盖 GET /admin/events/{event_id}/release-health。
func TestContractAdminReleaseHealth(t *testing.T) {
	runPathContract(t, http.MethodGet, "/admin/events/{event_id}/release-health")
}

// TestContractAdminCloseRegistration 覆盖 PATCH /admin/events/{event_id}/registration/close。
func TestContractAdminCloseRegistration(t *testing.T) {
	runPathContract(t, http.MethodPatch, "/admin/events/{event_id}/registration/close")
}

// TestContractAdminOpenRegistration 覆盖 PATCH /admin/events/{event_id}/registration/open。
func TestContractAdminOpenRegistration(t *testing.T) {
	runPathContract(t, http.MethodPatch, "/admin/events/{event_id}/registration/open")
}
