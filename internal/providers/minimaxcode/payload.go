package minimaxcode

import (
	"encoding/json"
	"strings"

	"github.com/caigee-cmd/cli2api/internal/translate"
)

// buildChatBody translates an OpenAI chat request into the Anthropic Messages
// body the MiniMax Code gateway expects.
func buildChatBody(req translate.ChatRequest, model string) (map[string]any, error) {
	systemParts := []string{}
	messages := make([]any, 0, len(req.Messages))
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system", "developer":
			if text := messageText(msg.Content); strings.TrimSpace(text) != "" {
				systemParts = append(systemParts, text)
			}
			continue
		case "tool", "function":
			messages = append(messages, map[string]any{
				"role": "user",
				"content": []any{map[string]any{
					"type": "tool_result", "tool_use_id": first(msg.ToolCallID, msg.Name, "tool"),
					"content": toolResultContent(msg),
				}},
			})
			continue
		case "assistant":
			blocks := make([]any, 0)
			if text := messageText(msg.Content); strings.TrimSpace(text) != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": text})
			}
			if len(msg.ToolCalls) > 0 {
				var calls []map[string]any
				if json.Unmarshal(msg.ToolCalls, &calls) == nil {
					for _, call := range calls {
						id, _ := call["id"].(string)
						function, _ := call["function"].(map[string]any)
						name, _ := function["name"].(string)
						arguments, _ := function["arguments"].(string)
						var input any = map[string]any{}
						if arguments != "" {
							_ = json.Unmarshal([]byte(arguments), &input)
						}
						blocks = append(blocks, map[string]any{"type": "tool_use", "id": first(id, newToolID()), "name": first(name, "tool"), "input": input})
					}
				}
			}
			if len(blocks) > 0 {
				messages = append(messages, map[string]any{"role": "assistant", "content": blocks})
			}
			continue
		default:
			messages = append(messages, map[string]any{"role": "user", "content": contentBlocks(msg.Content)})
		}
	}
	body := map[string]any{
		"model":      model,
		"max_tokens": requestMaxTokens(req.MaxTokens),
		"messages":   messages,
	}
	if len(systemParts) > 0 {
		body["system"] = strings.Join(systemParts, "\n\n")
	}
	if req.Stream {
		body["stream"] = true
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		if json.Unmarshal(req.Tools, &tools) == nil {
			anthropicTools := make([]any, 0, len(tools))
			for _, tool := range tools {
				typ, _ := tool["type"].(string)
				function, _ := tool["function"].(map[string]any)
				name, _ := function["name"].(string)
				if typ != "function" || name == "" {
					continue
				}
				description, _ := function["description"].(string)
				params := function["parameters"]
				if params == nil {
					params = map[string]any{"type": "object", "properties": map[string]any{}}
				}
				entry := map[string]any{"name": name, "input_schema": params}
				if description != "" {
					entry["description"] = description
				}
				anthropicTools = append(anthropicTools, entry)
			}
			if len(anthropicTools) > 0 {
				body["tools"] = anthropicTools
			}
		}
	}
	return body, nil
}

func messageText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		parts := make([]string, 0, len(value))
		for _, raw := range value {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := part["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func contentBlocks(content any) []any {
	switch value := content.(type) {
	case string:
		return []any{map[string]any{"type": "text", "text": value}}
	case []any:
		blocks := make([]any, 0, len(value))
		for _, raw := range value {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch typ, _ := part["type"].(string); typ {
			case "image_url":
				if block, ok := anthropicImageBlock(part); ok {
					blocks = append(blocks, block)
				}
			default:
				if text, ok := part["text"].(string); ok {
					blocks = append(blocks, map[string]any{"type": "text", "text": text})
				}
			}
		}
		if len(blocks) > 0 {
			return blocks
		}
	}
	return []any{map[string]any{"type": "text", "text": ""}}
}

// toolResultContent keeps tool output images inside the tool_result block,
// which is where Anthropic-compatible upstreams expect them. Without this a
// screenshot-returning tool degrades to text only.
func toolResultContent(msg translate.ChatMessage) any {
	text := messageText(msg.Content)
	if len(msg.ToolResultImages) == 0 {
		return text
	}
	blocks := make([]any, 0, len(msg.ToolResultImages)+1)
	if strings.TrimSpace(text) != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	for _, raw := range msg.ToolResultImages {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if block, ok := anthropicImageBlock(part); ok {
			blocks = append(blocks, block)
		}
	}
	if len(blocks) == 0 {
		return text
	}
	return blocks
}

// anthropicImageBlock converts an OpenAI-style image part
// ({"type":"image_url","image_url":{"url":...}}) into an Anthropic image block.
func anthropicImageBlock(part map[string]any) (map[string]any, bool) {
	if blockType, _ := part["type"].(string); blockType == "image" {
		// Already Anthropic-shaped.
		if source, ok := part["source"].(map[string]any); ok {
			return map[string]any{"type": "image", "source": source}, true
		}
		return nil, false
	}
	url := imageURLValue(part["image_url"])
	if url == "" {
		return nil, false
	}
	if mediaType, data, ok := strings.Cut(strings.TrimPrefix(url, "data:"), ";base64,"); ok {
		return map[string]any{
			"type":   "image",
			"source": map[string]any{"type": "base64", "media_type": mediaType, "data": data},
		}, true
	}
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": url}}, true
	}
	return nil, false
}

func imageURLValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		url, _ := typed["url"].(string)
		return url
	}
	return ""
}

// requestMaxTokens forwards the client's max_tokens verbatim; the upstream
// protocol requires the field, so an absent value falls back to 8192 only
// as a protocol default, never as a catalog maximum.
func requestMaxTokens(raw json.RawMessage) int {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return 8192
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil || value <= 0 {
		return 8192
	}
	return value
}

func newToolID() string {
	return "toolu_" + randomHex(8)
}
