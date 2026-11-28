package translate

import (
	"encoding/json"
	"testing"
)

func TestMaterializeToolResultImages(t *testing.T) {
	req := ChatRequest{Messages: []ChatMessage{
		{Role: "user", Content: "check the page"},
		{Role: "assistant", ToolCalls: json.RawMessage(`[{"id":"call_1","type":"function","function":{"name":"screenshot","arguments":"{}"}}]`)},
		{
			Role: "tool", ToolCallID: "call_1", Content: "page rendered",
			ToolResultImages: []any{map[string]any{
				"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAAA"},
			}},
		},
	}}
	out := MaterializeToolResultImages(req)
	if len(out.Messages) != 4 {
		t.Fatalf("messages = %d, want 4 (tool result followed by image turn)", len(out.Messages))
	}
	last := out.Messages[3]
	if last.Role != "user" {
		t.Fatalf("last role = %q, want user", last.Role)
	}
	parts, ok := last.Content.([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("last content = %#v, want text + image", last.Content)
	}
	image, _ := parts[1].(map[string]any)
	url, _ := image["image_url"].(map[string]any)
	if url["url"] != "data:image/png;base64,AAAA" {
		t.Fatalf("image url lost: %#v", image)
	}
}

func TestMaterializeToolResultImagesLeavesPlainRequestsAlone(t *testing.T) {
	req := ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}}
	out := MaterializeToolResultImages(req)
	if len(out.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(out.Messages))
	}
}
