package workers

import (
	"encoding/json"
	"testing"
)

func TestRequestSerialization(t *testing.T) {
	req := Request{
		ID:     "req-1",
		Action: "send_code",
		Payload: map[string]interface{}{
			"phone_number": "+1234567890",
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	var parsed Request
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}

	if parsed.ID != "req-1" || parsed.Action != "send_code" {
		t.Fatalf("mismatched request fields: %+v", parsed)
	}
}

func TestResponseDeserialization(t *testing.T) {
	raw := `{"id":"req-2","status":"need_otp","data":{"phone_code_hash":"xyz123"}}`

	var resp Response
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Status != "need_otp" {
		t.Fatalf("expected status need_otp, got %s", resp.Status)
	}

	hash, ok := resp.Data["phone_code_hash"].(string)
	if !ok || hash != "xyz123" {
		t.Fatalf("expected hash xyz123, got %v", resp.Data)
	}
}
