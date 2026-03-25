package file

import (
	"context"
	"encoding/json"
	"testing"
)

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func mustUnmarshal(t *testing.T, data json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func mustExecute(t *testing.T, tool interface {
	Execute(context.Context, json.RawMessage) (json.RawMessage, error)
}, input any) json.RawMessage {
	t.Helper()
	result, err := tool.Execute(context.Background(), mustMarshal(t, input))
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	return result
}
