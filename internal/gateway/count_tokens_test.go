package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEstimateAnthropicInputTokens(t *testing.T) {
	body := `{
		"model": "test-flash",
		"system": [{"type":"text","text":"You are a careful agent."}],
		"messages": [
			{"role":"user","content":[{"type":"text","text":"看一看这张图"}]},
			{"role":"user","content":[
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + strings.Repeat("aGVsbG8=", 200) + `"}}
			]}
		],
		"tools": [{"name":"read","description":"read a file","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}],
		"max_tokens": 1024
	}`
	tokens, err := estimateAnthropicInputTokens([]byte(body))
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	// The base64 blob must be billed as one image, not as ~2400 characters.
	if tokens < imageTokenEstimate {
		t.Fatalf("tokens = %d, want at least one image estimate %d", tokens, imageTokenEstimate)
	}
	if tokens > imageTokenEstimate+200 {
		t.Fatalf("tokens = %d, text seems double counted around the image", tokens)
	}
}

func TestEstimateAnthropicInputTokensCountsText(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"user","content":"hello world"}]}`
	tokens, err := estimateAnthropicInputTokens([]byte(body))
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if tokens < 2 || tokens > 20 {
		t.Fatalf("tokens = %d, want a small positive estimate", tokens)
	}
}

func TestHandleAnthropicCountTokens(t *testing.T) {
	handler := &Handler{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(
		`{"model":"m","messages":[{"role":"user","content":"hello"}]}`,
	))
	handler.HandleAnthropicCountTokens(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"input_tokens":`) {
		t.Fatalf("body = %s, want input_tokens", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/v1/messages/count_tokens", nil)
	handler.HandleAnthropicCountTokens(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader("not-json"))
	handler.HandleAnthropicCountTokens(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("bad body status = %d, want 400", recorder.Code)
	}
}
