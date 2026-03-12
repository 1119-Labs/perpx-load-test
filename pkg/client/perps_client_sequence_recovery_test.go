package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPerpxPerpsClient_RecoverSequence_NoOpIfNeverQueried(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Simulate a client that never queried account state.
	client.accountQueryMtx.Lock()
	client.accountQueried = false
	client.accountQueryMtx.Unlock()

	err := client.recoverSequence()
	require.NoError(t, err)

	metrics := client.GetErrorMetrics()
	require.Equal(t, uint64(0), metrics["sequence_mismatches"])
}

func TestPerpxPerpsClient_RecoverSequence_RequeriesAndUpdatesSequence(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// New REST server that returns a different sequence.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := mockAccountResponse{}
		resp.Account.Type = "/cosmos.auth.v1beta1.BaseAccount"
		resp.Account.Address = "perpx1testaddressxxxxxxxxxxxxxxxxxxxxxx"
		resp.Account.AccountNumber = "10"
		resp.Account.Sequence = "42"
		require.NoError(t, json.NewEncoder(w).Encode(&resp))
	}))
	t.Cleanup(server.Close)

	client.restURL = server.URL

	err := client.recoverSequence()
	require.NoError(t, err)
	require.Equal(t, uint64(42), client.sequence)

	metrics := client.GetErrorMetrics()
	require.Equal(t, uint64(1), metrics["sequence_mismatches"])
	byType := metrics["error_counts_by_type"].(map[string]uint64)
	require.Equal(t, uint64(1), byType["sequence_recovered"])
}

func TestPerpxPerpsClient_RecoverSequence_FailureIncrementsMetrics(t *testing.T) {
	client, _ := newTestPerpsClient(t)

	// Server always fails.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	client.restURL = server.URL

	err := client.recoverSequence()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to recover sequence")

	metrics := client.GetErrorMetrics()
	byType := metrics["error_counts_by_type"].(map[string]uint64)
	require.GreaterOrEqual(t, byType["sequence_recovery_failed"], uint64(1))
	require.GreaterOrEqual(t, metrics["account_query_failures"].(uint64), uint64(1))
}


