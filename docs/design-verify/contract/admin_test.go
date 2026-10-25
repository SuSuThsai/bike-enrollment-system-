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
