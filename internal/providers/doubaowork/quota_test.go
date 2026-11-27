package doubaowork

import (
	"context"
	"net/http"
	"testing"
)

func quotaHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/passport/account/info/v2/", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, map[string]any{"user_id_str": "42", "screen_name": "Alice"})
	})
	mux.HandleFunc("/alice/user/launch", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, map[string]any{"assistant_bot_id": "998877"})
	})
	mux.HandleFunc("/alice/commerce/sale/subscription/quota/summary/", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, map[string]any{
			"window_limit_section": map[string]any{
				"usage_exhausted": false,
				"window_limit_groups": []any{
					map[string]any{
						"feature_group_name": "订阅额度",
						"window_limits": []any{
							map[string]any{"used_amount": 0, "total_amount": 500, "used_percent": 0, "end_time": 4102444800},
						},
					},
				},
			},
		})
	})
	mux.HandleFunc("/alice/commerce/sale/subscription/overview/", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, map[string]any{"current_subscription": map[string]any{"display": map[string]any{"short_name": "豆包工作 Pro"}}})
	})
	mux.HandleFunc("/alice/commerce/marketing/card/list/", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, map[string]any{"data": map[string]any{"cards": []any{
			map[string]any{"source_display_text": "活动赠送", "track_params": map[string]any{"card_balance": 5}},
		}}})
	})
	return mux
}

func TestQuotaAggregatesSources(t *testing.T) {
	client := newChatClient(t, quotaHandler())
	info, err := client.Quota(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("quota: %v", err)
	}
	if info == nil {
		t.Fatal("nil quota")
	}
	if info.Plan != "豆包工作 Pro" {
		t.Fatalf("plan = %q", info.Plan)
	}
	// subscription window + activity card
	if len(info.Windows) < 2 {
		t.Fatalf("expected >=2 windows, got %d: %+v", len(info.Windows), info.Windows)
	}
}

func TestQuotaDegradesWhenSummaryMissing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/passport/account/info/v2/", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, map[string]any{"user_id_str": "42"})
	})
	mux.HandleFunc("/alice/user/launch", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, map[string]any{"assistant_bot_id": "998877"})
	})
	// summary/overview/cards all fail with 500.
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	client := newChatClient(t, mux)
	info, err := client.Quota(context.Background(), "acc1")
	if err != nil {
		t.Fatalf("quota must degrade, not error: %v", err)
	}
	if info == nil || info.ProviderID != "doubaowork" {
		t.Fatalf("expected non-nil degraded quota, got %+v", info)
	}
}
