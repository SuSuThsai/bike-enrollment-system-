package contract

import (
	"net/http"
	"testing"
)

// TestContractEnrollmentLock 覆盖 POST /enrollments/lock。
func TestContractEnrollmentLock(t *testing.T) {
	runPathContract(t, http.MethodPost, "/enrollments/lock")
}

// TestContractEnrollmentSubmit 覆盖 POST /enrollments。
func TestContractEnrollmentSubmit(t *testing.T) {
	runPathContract(t, http.MethodPost, "/enrollments")
}

// TestContractEnrollmentListMine 覆盖 GET /enrollments/my。
func TestContractEnrollmentListMine(t *testing.T) {
	runPathContract(t, http.MethodGet, "/enrollments/my")
}

// TestContractEnrollmentDetail 覆盖 GET /enrollments/{enrollment_id}。
func TestContractEnrollmentDetail(t *testing.T) {
	runPathContract(t, http.MethodGet, "/enrollments/{enrollment_id}")
}
