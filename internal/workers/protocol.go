package workers

// Request represents an IPC command sent from the Go host to the Python worker via stdin.
type Request struct {
	ID      string                 `json:"id"`
	Action  string                 `json:"action"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

// Response represents an IPC response returned by the Python worker via stdout.
type Response struct {
	ID     string                 `json:"id"`
	Status string                 `json:"status"` // "ok", "error", "need_otp", "need_password", "qr_code"
	Data   map[string]interface{} `json:"data,omitempty"`
	Error  string                 `json:"error,omitempty"`
}
