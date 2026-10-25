package contract

import (
	"net/http"
	"testing"
)

// TestContractEventsList 覆盖 GET /events。
func TestContractEventsList(t *testing.T) {
	runPathContract(t, http.MethodGet, "/events")
}

// TestContractEventDetail 覆盖 GET /events/{event_id}。
func TestContractEventDetail(t *testing.T) {
	runPathContract(t, http.MethodGet, "/events/{event_id}")
}
