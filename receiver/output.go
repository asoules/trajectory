package main

import "strings"

// Count/display textual evidence, never base64 screenshots or audio payloads.
// A command's recorded stdout may exceed what the agent actually received.
func outputText(raw any) string {
	if raw == nil {
		return ""
	}
	if list, ok := raw.([]any); ok {
		parts := []string{}
		for _, item := range list {
			v := obj(item)
			switch str(v["type"]) {
			case "text", "input_text", "output_text":
				parts = append(parts, str(v["text"]))
			case "image", "input_image", "audio", "input_audio", "resource_link":
				continue
			default:
				if len(v) == 0 {
					parts = append(parts, str(item))
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	if m := obj(raw); len(m) > 0 {
		if m["content"] != nil {
			return outputText(m["content"])
		}
		if m["type"] == "image" || m["type"] == "audio" {
			return ""
		}
	}
	return str(raw)
}

func messageText(text string) string {
	text = strings.TrimSpace(text)
	// These are known host-supplied wrappers, not the user's task description.
	for _, tag := range []string{"in-app-browser-context", "environment_context", "app-context"} {
		if strings.HasPrefix(text, "<"+tag) {
			close := "</" + tag + ">"
			if end := strings.Index(text, close); end >= 0 {
				text = strings.TrimSpace(text[end+len(close):])
			}
		}
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, "## My request:"))
	if strings.HasPrefix(text, "<") {
		return ""
	}
	return clip(text, 4000)
}

func messageLabel(text string) string {
	return clip(strings.Join(strings.Fields(messageText(text)), " "), 100)
}
