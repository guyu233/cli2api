package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/http"
)

// HandleAnthropicCountTokens serves POST /v1/messages/count_tokens.
//
// Claude Code calls this endpoint before a turn to size its context window
// and to decide when to auto-compact. The gateway has no tokenizer of its
// own, so the estimate is deliberately conservative: ASCII text is billed
// at roughly one token per 3.5 bytes, non-ASCII text at one token per
// character (CJK tokenizers are near 1:1), and every image block counts as
// a fixed 1500 tokens (a 1600x900 screenshot is ~1920 at Anthropic's
// pixels/750 rule). Over-counting makes a client compact slightly early;
// under-counting would let it overflow mid-turn, which is far worse.
func (h *Handler) HandleAnthropicCountTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAnthropicError(w, http.StatusMethodNotAllowed, "invalid_request_error", "POST only")
		return
	}
	body := decodeRequestBody(w, r, maxChatRequestBytes, maxChatRequestBytes)
	defer body.Close()
	if body.err != nil {
		writeAnthropicRequestBodyError(w, body.err)
		return
	}
	raw, readErr := readConversationBody(body, maxChatRequestBytes)
	if readErr != nil {
		if isRequestBodyTooLarge(readErr) {
			writeAnthropicRequestBodyError(w, readErr)
			return
		}
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", readErr.Error())
		return
	}
	tokens, err := estimateAnthropicInputTokens(raw)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"input_tokens": tokens})
}

// estimateAnthropicInputTokens walks every string in the request body, so
// system prompts, message text, tool descriptions and tool schemas are all
// counted without the gateway having to model each content-block variant.
func estimateAnthropicInputTokens(body []byte) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return 0, err
	}
	if _, ok := root.(map[string]any); !ok {
		return 0, errors.New("request body must be a JSON object")
	}
	counter := &tokenEstimator{}
	counter.walk(root)
	return counter.tokens(), nil
}

const (
	// imageTokenEstimate mirrors Anthropic's ~1500-token cost of a
	// full-window image; exact dimensions are not available here.
	imageTokenEstimate = 1500
	// asciiBytesPerToken is a conservative BPE ratio for mixed prose and
	// JSON tool schemas.
	asciiBytesPerToken = 3.5
)

type tokenEstimator struct {
	ascii    int
	nonASCII int
	images   int
}

func (e *tokenEstimator) tokens() int {
	asciiTokens := int(math.Ceil(float64(e.ascii) / asciiBytesPerToken))
	total := asciiTokens + e.nonASCII + e.images*imageTokenEstimate
	if total < 1 {
		return 1
	}
	return total
}

func (e *tokenEstimator) walk(value any) {
	switch typed := value.(type) {
	case map[string]any:
		// Media blocks are recognized before their base64 payload is visited,
		// otherwise a single screenshot would be billed as its raw byte count.
		if isMediaBlock(typed) {
			e.images++
			return
		}
		for key, child := range typed {
			e.countText(key)
			e.walk(child)
		}
	case []any:
		for _, child := range typed {
			e.walk(child)
		}
	case string:
		if isBase64Payload(typed) {
			e.images++
			return
		}
		e.countText(typed)
	case json.Number, bool, nil:
		// Numbers and booleans carry no prompt text worth counting.
	}
}

func (e *tokenEstimator) countText(text string) {
	for _, r := range text {
		if r < 128 {
			e.ascii++
			continue
		}
		e.nonASCII++
	}
}

// isMediaBlock spots Anthropic image blocks and OpenAI/Responses image parts
// ({"type":"image"}, {"type":"image_url"} and {"type":"input_image"}).
func isMediaBlock(block map[string]any) bool {
	typ, _ := block["type"].(string)
	switch typ {
	case "image", "image_url", "input_image":
		return true
	}
	return false
}

// isBase64Payload distinguishes inline image bytes from ordinary text. Base64
// has no whitespace and only [A-Za-z0-9+/=]; prose and code always contain
// other punctuation well before 512 characters.
func isBase64Payload(text string) bool {
	if len(text) < 512 {
		return false
	}
	for _, r := range text {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '+', r == '/', r == '=':
		default:
			return false
		}
	}
	return true
}
