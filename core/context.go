package core

import (
	"encoding/json"
	"errors"
)

// SelectContext drops old assistant/tool groups atomically. System and user
// messages are retained; bytes are a transport guard, not a token estimator.
func SelectContext(history []Message, limit int) ([]Message, error) {
	data, err := json.Marshal(history)
	if err != nil {
		return nil, err
	}
	var selected []Message
	if err := json.Unmarshal(data, &selected); err != nil {
		return nil, err
	}
	for len(data) > limit {
		start := -1
		for index, message := range selected {
			if message.Role == "assistant" && index < len(selected)-1 {
				start = index
				break
			}
		}
		if start < 0 {
			return nil, errors.New("required context exceeds configured byte limit")
		}
		end := start + 1
		for end < len(selected) && selected[end].Role == "tool" {
			end++
		}
		// Preserve the newest assistant/result group as an actionable observation.
		if end == len(selected) {
			return nil, errors.New("latest observation exceeds context limit")
		}
		selected = append(selected[:start], selected[end:]...)
		data, err = json.Marshal(selected)
		if err != nil {
			return nil, err
		}
	}
	return selected, nil
}
