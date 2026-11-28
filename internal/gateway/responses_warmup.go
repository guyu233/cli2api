package gateway

import (
	"net/http"
	"time"

	"github.com/caigee-cmd/cli2api/internal/accounts"
	"github.com/caigee-cmd/cli2api/internal/translate"
)

// handleResponsesWarmup answers Codex CLI's startup probe. The request has no
// conversation input, so there is nothing to send upstream; returning a
// synthetic completed response both keeps the probe cheap (zero tokens) and
// avoids a provider 400 on an empty prompt.
func (h *Handler) handleResponsesWarmup(w http.ResponseWriter, native *translate.NativeResponsesRequest) {
	requestID := accounts.NewRequestID()
	response := map[string]any{
		"id": "resp_" + requestID, "object": "response", "created_at": time.Now().Unix(),
		"status": "completed", "model": native.Model(), "output": []any{},
		"usage": map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
	}
	w.Header().Set("X-Request-Id", requestID)
	if !native.Stream() {
		writeJSON(w, http.StatusOK, response)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	writer := responsesEventWriter{writer: w}
	_ = writer.write("response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id": response["id"], "object": "response", "created_at": response["created_at"],
			"status": "in_progress", "model": native.Model(), "output": []any{},
		},
	})
	_ = writer.write("response.completed", map[string]any{"type": "response.completed", "response": response})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}
