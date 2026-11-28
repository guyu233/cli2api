package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/caigee-cmd/cli2api/internal/accounts"
	"github.com/caigee-cmd/cli2api/internal/app"
	"github.com/caigee-cmd/cli2api/internal/auth"
	"github.com/caigee-cmd/cli2api/internal/executor"
	apigateway "github.com/caigee-cmd/cli2api/internal/gateway"
)

type aggregationHTTPResolver struct {
	agg accounts.Aggregation
}

func (r aggregationHTTPResolver) Resolve(_ context.Context, id string, _ map[string]bool) (accounts.Aggregation, error) {
	if id != r.agg.ID {
		return accounts.Aggregation{}, accounts.ErrAggregationNotFound
	}
	return r.agg, nil
}

func TestAggregationSameAccountMemberFailoverThroughHandler(t *testing.T) {
	var mu sync.Mutex
	var seenModels []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		mu.Lock()
		seenModels = append(seenModels, payload.Model)
		mu.Unlock()
		if payload.Model == "model-a" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"type":"rate_limit_error","message":"model-a is rate limited"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"model":"model-b","choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
	}))
	defer upstream.Close()

	pool := executor.NewPool(nil, nil)
	pool.Upsert(executor.Item{
		ID: "account-a", URL: upstream.URL, Provider: "qoder", Region: "global",
		Models: []string{"model-a", "model-b"}, Runtime: "child_process",
	})
	chatExecutor := executor.NewChatExecutor(pool, "")
	chatExecutor.HTTPClient = upstream.Client()
	resolver := aggregationHTTPResolver{agg: accounts.Aggregation{
		ID: "logical-model", Enabled: true, Strategy: accounts.AggregationPriority,
		AllowFallbacks: true, MaxAttempts: 2,
		Members: []accounts.AggregationMember{
			{Provider: "qoder", Model: "model-a", Priority: 0, Weight: 100},
			{Provider: "qoder", Model: "model-b", Priority: 1, Weight: 100},
		},
	}}
	var crossProvider atomic.Bool
	crossProvider.Store(true)
	gatewayHandler := &apigateway.Handler{
		Executor: chatExecutor, Pool: pool, Aggregations: resolver,
		CrossProviderPool: &crossProvider, KeyLimits: &apigateway.KeyInFlight{},
	}
	server := &Server{App: &app.App{Executor: chatExecutor, Pool: pool, Gateway: gatewayHandler}}
	identity := auth.Identity{
		Kind: auth.KindKey, KeyID: "key-agg", Name: "agg",
		AllowedProviders: []string{"qoder"}, ModelAggregation: true,
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"logical-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithIdentity(req.Context(), identity))
	rec := httptest.NewRecorder()
	server.handleChatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Model != "logical-model" || len(body.Choices) != 1 || body.Choices[0].Message.Content != "hello" {
		t.Fatalf("body=%s", rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seenModels) != 2 || seenModels[0] != "model-a" || seenModels[1] != "model-b" {
		t.Fatalf("upstream attempts=%v, want [model-a model-b]", seenModels)
	}
}

func TestAggregationKeyProtocolMatrixThroughHandler(t *testing.T) {
	var mu sync.Mutex
	var seenModels []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("upstream path=%s", r.URL.Path)
		}
		var payload struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		mu.Lock()
		seenModels = append(seenModels, payload.Model)
		mu.Unlock()
		if payload.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, strings.Join([]string{
				`data: {"id":"1","object":"chat.completion.chunk","model":"member-a","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`,
				"",
				`data: {"id":"1","object":"chat.completion.chunk","model":"member-a","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
				"",
				`data: [DONE]`,
				"",
			}, "\n"))
			return
		}
		_, _ = io.WriteString(w, `{"model":"member-a","choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
	}))
	defer upstream.Close()

	pool := executor.NewPool(nil, nil)
	pool.Upsert(executor.Item{ID: "account-a", URL: upstream.URL, Provider: "qoder", Region: "global", Models: []string{"model-a"}, Runtime: "child_process"})
	pool.Upsert(executor.Item{ID: "account-b", URL: upstream.URL, Provider: "qoder", Region: "global", Models: []string{"model-b"}, Runtime: "child_process"})
	chatExecutor := executor.NewChatExecutor(pool, "")
	chatExecutor.HTTPClient = upstream.Client()
	resolver := aggregationHTTPResolver{agg: accounts.Aggregation{
		ID: "logical-model", Enabled: true, Strategy: accounts.AggregationPriority,
		AllowFallbacks: true, MaxAttempts: 2,
		Members: []accounts.AggregationMember{
			{Provider: "qoder", Model: "model-a", Priority: 0, Weight: 100},
			{Provider: "qoder", Model: "model-b", Priority: 1, Weight: 100},
		},
	}}
	var crossProvider atomic.Bool
	crossProvider.Store(true)
	gatewayHandler := &apigateway.Handler{
		Executor:          chatExecutor,
		Pool:              pool,
		Aggregations:      resolver,
		CrossProviderPool: &crossProvider,
		KeyLimits:         &apigateway.KeyInFlight{},
	}
	server := &Server{App: &app.App{
		Executor: chatExecutor, Pool: pool, Gateway: gatewayHandler,
	}}
	identity := auth.Identity{
		Kind: auth.KindKey, KeyID: "key-agg", Name: "agg",
		AllowedProviders: []string{"qoder"}, ModelAggregation: true,
	}

	call := func(t *testing.T, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		req = req.WithContext(auth.WithIdentity(req.Context(), identity))
		rec := httptest.NewRecorder()
		switch path {
		case "/v1/chat/completions":
			server.handleChatCompletions(rec, req)
		case "/v1/messages":
			server.handleAnthropicMessages(rec, req)
		case "/v1/responses":
			server.handleResponses(rec, req)
		default:
			t.Fatalf("unknown path %s", path)
		}
		return rec
	}

	openai := call(t, "/v1/chat/completions", `{"model":"logical-model","messages":[{"role":"user","content":"hi"}]}`, nil)
	if openai.Code != http.StatusOK {
		t.Fatalf("openai status=%d body=%s", openai.Code, openai.Body.String())
	}
	var oa struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(openai.Body.Bytes(), &oa)
	if oa.Model != "logical-model" {
		t.Fatalf("openai model=%q body=%s", oa.Model, openai.Body.String())
	}

	stream := call(t, "/v1/chat/completions", `{"model":"logical-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`, nil)
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "logical-model") || strings.Contains(stream.Body.String(), "member-a") {
		t.Fatalf("stream status=%d body=%s", stream.Code, stream.Body.String())
	}

	anthropic := call(t, "/v1/messages", `{"model":"logical-model","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`, map[string]string{"anthropic-version": "2023-06-01"})
	if anthropic.Code != http.StatusOK {
		t.Fatalf("anthropic status=%d body=%s", anthropic.Code, anthropic.Body.String())
	}
	var an struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(anthropic.Body.Bytes(), &an)
	if an.Model != "logical-model" {
		t.Fatalf("anthropic model=%q body=%s", an.Model, anthropic.Body.String())
	}

	responses := call(t, "/v1/responses", `{"model":"logical-model","input":"hi"}`, nil)
	if responses.Code != http.StatusOK {
		t.Fatalf("responses status=%d body=%s", responses.Code, responses.Body.String())
	}
	var rs struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(responses.Body.Bytes(), &rs)
	if rs.Model != "logical-model" {
		t.Fatalf("responses model=%q body=%s", rs.Model, responses.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seenModels) == 0 {
		t.Fatal("upstream saw no requests")
	}
	for _, model := range seenModels {
		if model != "model-a" && model != "model-b" {
			t.Fatalf("upstream model=%q, want a concrete member", model)
		}
	}
	if !strings.Contains(strings.Join(seenModels, ","), "model-a") {
		t.Fatalf("aggregation did not dispatch the priority member: %v", seenModels)
	}
}
