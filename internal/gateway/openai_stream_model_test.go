package gateway

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRelayOpenAIStreamRewritesModel(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-x","object":"chat.completion.chunk","model":"test-real","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl-x","object":"chat.completion.chunk","model":"test-real","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")
	rec := httptest.NewRecorder()
	stats, err := RelayOpenAIStream(rec, strings.NewReader(body), "test-agg")
	if err != nil {
		t.Fatal(err)
	}
	if !stats.SawDone {
		t.Fatal("expected [DONE] to be relayed")
	}
	out := rec.Body.String()
	if !strings.Contains(out, `"model":"test-agg"`) {
		t.Fatalf("stream did not echo the public model: %s", out)
	}
	if strings.Contains(out, "test-real") {
		t.Fatalf("upstream member model leaked: %s", out)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("terminator missing: %s", out)
	}
}
