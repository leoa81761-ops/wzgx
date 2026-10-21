package gateway

import (
	"encoding/json"
	"errors"
)

// anthropicBody builds a streamed Messages API request. System messages go to "system".
// max_tokens comes from Params (the API requires it); 16000 if unset.
func anthropicBody(cfg Config, req Request) ([]byte, error) {
	m := map[string]any{}
	for k, v := range cfg.Params {
		m[k] = v
	}
	var sys string
	msgs := []Message{}
	for _, x := range req.Messages {
		if x.Role == "system" {
			if sys != "" {
				sys += "\n\n"
			}
			sys += x.Content
			continue
		}
		msgs = append(msgs, x)
	}
	m["model"] = cfg.Model
	m["messages"] = msgs
	m["stream"] = true
	if sys != "" {
		m["system"] = sys
	}
	if req.MaxTokens > 0 {
		m["max_tokens"] = req.MaxTokens
	}
	if _, ok := m["max_tokens"]; !ok {
		m["max_tokens"] = 16000
	}
	return json.Marshal(m)
}

type anthEvent struct {
	text, model, finish string
	in, out             int
	stop                bool
}

// parseAnthropic reads one SSE data payload. Thinking deltas are ignored as text but still
// count as bytes for the idle timer (handled by the caller).
func parseAnthropic(data []byte) (anthEvent, error) {
	var e struct {
		Type    string `json:"type"`
		Message struct {
			Model string `json:"model"`
			Usage struct {
				InputTokens int `json:"input_tokens"`
			} `json:"usage"`
		} `json:"message"`
		Delta struct {
			Type       string `json:"type"`
			Text       string `json:"text"`
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	var out anthEvent
	if json.Unmarshal(data, &e) != nil {
		return out, nil
	}
	switch e.Type {
	case "message_start":
		out.model, out.in = e.Message.Model, e.Message.Usage.InputTokens
	case "content_block_delta":
		if e.Delta.Type == "text_delta" {
			out.text = e.Delta.Text
		}
	case "message_delta":
		out.out = e.Usage.OutputTokens
		switch e.Delta.StopReason {
		case "end_turn", "stop_sequence":
			out.finish = "stop"
		case "max_tokens":
			out.finish = "length"
		case "":
		default:
			out.finish = e.Delta.StopReason
		}
	case "message_stop":
		out.stop = true
	case "error":
		return out, errors.New("upstream error: " + e.Error.Message)
	}
	return out, nil
}
