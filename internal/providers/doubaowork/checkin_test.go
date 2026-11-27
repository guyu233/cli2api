package doubaowork

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

const testCampaignGroup = "pc_incentive_task_mvp_v3"

type checkinServer struct {
	mu          sync.Mutex
	checked     bool
	taskStatus  int
	claimCode   int
	claimNested bool
	claimCalls  int
	includeTask bool
	group       string
	auth        bool
}

func newCheckinServer() *checkinServer {
	return &checkinServer{taskStatus: 1, includeTask: true, group: testCampaignGroup}
}

func (s *checkinServer) detail(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.URL.Query().Get("campaign_group_code") == "" {
		http.Error(w, "missing group", http.StatusBadRequest)
		return
	}
	if !s.includeTask {
		writeEnvelope(w, map[string]any{"tasks": []any{}})
		return
	}
	task := map[string]any{
		"campaign_group_code": s.group,
		"task_status":         s.taskStatus,
		"daily_checkin": map[string]any{
			"checked_in_today": s.checked,
			"continuous_days":  3,
			"reward":           map[string]any{"reward_amount": 10},
		},
	}
	writeEnvelope(w, map[string]any{"tasks": []any{task}})
}

func (s *checkinServer) claim(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimCalls++
	if s.auth {
		http.Error(w, `{"code":710012001,"message":"login expired"}`, http.StatusUnauthorized)
		return
	}
	if s.claimCode != 0 {
		w.Header().Set("Content-Type", "application/json")
		if s.claimNested {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"error_code": s.claimCode}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": s.claimCode, "message": "claim rejected"})
		return
	}
	s.checked = true
	writeEnvelope(w, map[string]any{"success": true})
}

func (s *checkinServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/passport/account/info/v2/", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, map[string]any{"user_id_str": "42", "screen_name": "Alice"})
	})
	mux.HandleFunc("/alice/user/launch", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, map[string]any{
			"assistant_bot_id": "998877",
			"country":          "CN",
			"ug_campaign_info": map[string]any{"campaign_group_code": testCampaignGroup},
			"config":           map[string]any{"web_id": "555", "ttwid": "tw-1"},
		})
	})
	mux.HandleFunc("/alice/ug/campaign/v3/detail/", s.detail)
	mux.HandleFunc("/alice/ug/campaign/landing/checkin", s.claim)
	return mux
}

func newCheckinClient(t *testing.T, srv *checkinServer) (*Client, *fakeStore) {
	t.Helper()
	client, store := newTestClient(t, srv.handler())
	store.payload = credentialPayload(t, "")
	return client, store
}

func TestCheckinStatusIsReadOnlyAndReportsAlready(t *testing.T) {
	srv := newCheckinServer()
	srv.checked = true
	client, store := newCheckinClient(t, srv)

	result, err := client.CheckinStatus(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if result.Status != "already" || result.Streak != 3 || result.RewardCredits != 10 {
		t.Fatalf("unexpected status: %+v", result)
	}
	if srv.claimCalls != 0 || store.begins != 0 {
		t.Fatalf("status performed a write: claim=%d begin=%d", srv.claimCalls, store.begins)
	}
}

func TestCheckinStatusPendingDoesNotClaim(t *testing.T) {
	srv := newCheckinServer()
	client, store := newCheckinClient(t, srv)

	result, err := client.CheckinStatus(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if result.Status != "pending" {
		t.Fatalf("status = %+v", result)
	}
	if srv.claimCalls != 0 || store.begins != 0 {
		t.Fatalf("status performed a write: claim=%d begin=%d", srv.claimCalls, store.begins)
	}
}

func TestCheckinClaimsOnceAndReadsBack(t *testing.T) {
	srv := newCheckinServer()
	client, store := newCheckinClient(t, srv)

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if result.Status != "success" || result.RewardCredits != 10 {
		t.Fatalf("result = %+v", result)
	}
	if srv.claimCalls != 1 || store.begins != 1 || store.completes != 1 {
		t.Fatalf("claim=%d begin=%d complete=%d", srv.claimCalls, store.begins, store.completes)
	}

	// A second call in the same process must read back, not claim again.
	result, err = client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("second checkin: %v", err)
	}
	if result.Status != "already" {
		t.Fatalf("second result = %+v", result)
	}
	if srv.claimCalls != 1 || store.begins != 1 {
		t.Fatalf("second call wrote again: claim=%d begin=%d", srv.claimCalls, store.begins)
	}
}

func TestCheckinCrossClientUsesPersistentClaim(t *testing.T) {
	srv := newCheckinServer()
	client, store := newCheckinClient(t, srv)
	if _, err := client.Checkin(context.Background(), "acc1"); err != nil {
		t.Fatalf("first checkin: %v", err)
	}

	// Simulate a gateway restart with the same store: status is pending again
	// (the fake upstream remains unchecked), but the persisted slot must block.
	srv.mu.Lock()
	srv.checked = false
	srv.mu.Unlock()
	store2 := store
	client2 := NewClient(store2)
	client2.baseURL = client.baseURL
	result, err := client2.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("second client: %v", err)
	}
	if result.Status != "pending" {
		t.Fatalf("second client result = %+v", result)
	}
	if srv.claimCalls != 1 {
		t.Fatalf("persistent guard failed: claim calls = %d", srv.claimCalls)
	}
}

func TestCheckinSDKRequiredIsTerminalForTheDay(t *testing.T) {
	srv := newCheckinServer()
	srv.claimCode = codeCheckinSDKRequired
	client, store := newCheckinClient(t, srv)

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if result.Status != "pending" || result.Message == "" {
		t.Fatalf("result = %+v", result)
	}
	if srv.claimCalls != 1 || store.begins != 1 || store.completes != 1 {
		t.Fatalf("claim=%d begin=%d complete=%d", srv.claimCalls, store.begins, store.completes)
	}
	if _, err := client.Checkin(context.Background(), "acc1"); err != nil {
		t.Fatalf("second checkin: %v", err)
	}
	if srv.claimCalls != 1 {
		t.Fatalf("retried after terminal SDK error: %d", srv.claimCalls)
	}
}

func TestCheckinNestedSDKCodeIsRecognized(t *testing.T) {
	srv := newCheckinServer()
	srv.claimCode = codeCheckinSDKRequired
	srv.claimNested = true
	client, _ := newCheckinClient(t, srv)

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if result.Status != "pending" || !strings.Contains(result.Message, "SDK") {
		t.Fatalf("result = %+v", result)
	}
}

func TestCheckinCaptchaLeavesPending(t *testing.T) {
	srv := newCheckinServer()
	srv.claimCode = codeCaptcha
	client, _ := newCheckinClient(t, srv)

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if result.Status != "pending" {
		t.Fatalf("result = %+v", result)
	}
}

func TestCheckinAuthFailureReturnsError(t *testing.T) {
	srv := newCheckinServer()
	srv.auth = true
	client, _ := newCheckinClient(t, srv)

	result, err := client.Checkin(context.Background(), "acc1")
	if err == nil {
		t.Fatalf("expected auth error, result=%+v", result)
	}
}

func TestCheckinNoCampaignSkips(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/passport/account/info/v2/", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, map[string]any{"user_id_str": "42"})
	})
	mux.HandleFunc("/alice/user/launch", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, map[string]any{"assistant_bot_id": "998877"})
	})
	client, store := newTestClient(t, mux)
	store.payload = credentialPayload(t, "")

	result, err := client.CheckinStatus(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if result.Status != "skipped" {
		t.Fatalf("result = %+v", result)
	}
}

func TestCheckinNonClaimableTaskSkips(t *testing.T) {
	srv := newCheckinServer()
	srv.taskStatus = 0
	client, _ := newCheckinClient(t, srv)

	result, err := client.CheckinStatus(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if result.Status != "skipped" {
		t.Fatalf("result = %+v", result)
	}
}

func TestFindDailyCheckinIgnoresOtherTasks(t *testing.T) {
	root := map[string]any{
		"tasks": []any{
			map[string]any{"task_status": 1},
			map[string]any{"daily_checkin": map[string]any{"checked_in_today": true}},
		},
	}
	if _, ok := findDailyCheckin(root); !ok {
		t.Fatal("daily task not found")
	}
	if _, ok := findDailyCheckin(map[string]any{"tasks": []any{map[string]any{"task_status": 1}}}); ok {
		t.Fatal("unexpected daily task")
	}
}

func TestFindDailyCheckinPrefersTasksOverExamples(t *testing.T) {
	root := map[string]any{
		"example_cases": []any{
			map[string]any{"daily_checkin": map[string]any{"checked_in_today": true}},
		},
		"tasks": []any{
			map[string]any{"daily_checkin": map[string]any{"checked_in_today": false}},
		},
	}
	task, ok := findDailyCheckin(root)
	if !ok {
		t.Fatal("daily task not found")
	}
	daily, _ := task["daily_checkin"].(map[string]any)
	if checked, _ := daily["checked_in_today"].(bool); checked {
		t.Fatal("example task won over the real task list")
	}
}
