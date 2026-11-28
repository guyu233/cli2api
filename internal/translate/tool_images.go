package translate

import "strings"

// MaterializeToolResultImages rewrites a request so that images returned by a
// tool result (Anthropic tool_result image blocks, carried on
// ChatMessage.ToolResultImages) also appear as a trailing user turn.
//
// Adapters that understand ToolResultImages natively must not call this: they place the picture inside the tool result itself. Every
// other adapter builds its tool message from text only, so without this helper
// a Playwright-style screenshot silently never reaches the model. The image
// stays a normal user image part, which those adapters already know how to
// forward.
func MaterializeToolResultImages(req ChatRequest) ChatRequest {
	needs := false
	for _, message := range req.Messages {
		if len(message.ToolResultImages) > 0 {
			needs = true
			break
		}
	}
	if !needs {
		return req
	}
	messages := make([]ChatMessage, 0, len(req.Messages)+1)
	for _, message := range req.Messages {
		messages = append(messages, message)
		if len(message.ToolResultImages) == 0 {
			continue
		}
		parts := make([]any, 0, len(message.ToolResultImages)+1)
		// A short text part keeps strict content validators happy: some
		// upstreams reject an image-only content array.
		parts = append(parts, map[string]any{"type": "text", "text": "工具返回的图片："})
		for _, raw := range message.ToolResultImages {
			if part, ok := toolImagePart(raw); ok {
				parts = append(parts, part)
			}
		}
		if len(parts) <= 1 {
			continue
		}
		messages = append(messages, ChatMessage{Role: "user", Content: parts})
	}
	req.Messages = messages
	return req
}

// toolImagePart normalizes the image shapes translate produces
// ({"type":"image_url","image_url":{"url":...}}) and the shapes an OpenAI
// image part can carry, into one canonical image_url part.
func toolImagePart(raw any) (any, bool) {
	part, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	switch blockType, _ := part["type"].(string); blockType {
	case "image":
		source, ok := part["source"].(map[string]any)
		if !ok {
			return nil, false
		}
		url, _ := source["url"].(string)
		if url == "" {
			mediaType, _ := source["media_type"].(string)
			data, _ := source["data"].(string)
			if mediaType == "" || data == "" {
				return nil, false
			}
			url = "data:" + mediaType + ";base64," + data
		}
		return imageURLPart(url), true
	case "image_url", "input_image", "":
		value := part["image_url"]
		if value == nil {
			value = part["url"]
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) == "" {
				return nil, false
			}
			return imageURLPart(typed), true
		case map[string]any:
			url, _ := typed["url"].(string)
			if strings.TrimSpace(url) == "" {
				return nil, false
			}
			return imageURLPart(url), true
		}
	}
	return nil, false
}

func imageURLPart(url string) map[string]any {
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}
}
