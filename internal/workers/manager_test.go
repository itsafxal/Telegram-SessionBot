package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkerSurvivesContextCancellation(t *testing.T) {
	scriptPath := filepath.Join("..", "..", "workers", "pyrofork", "worker.py")

	// Create a short-lived transient context (just like in Telegram message handlers)
	initCtx, initCancel := context.WithTimeout(context.Background(), 5*time.Second)

	worker, err := StartWorker(initCtx, "python", scriptPath, "pyrofork_test", 5*time.Second)
	if err != nil {
		t.Skipf("python/worker not found in test environment: %v", err)
		return
	}
	defer worker.Close()

	if !worker.IsAlive() {
		t.Errorf("expected worker to be alive after start")
	}

	// 1. Send init command while initCtx is active
	resp, err := worker.SendCommand(initCtx, "init", map[string]interface{}{
		"api_id":   12345,
		"api_hash": "test_hash",
	})
	if err != nil {
		t.Fatalf("failed to init worker: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("expected ok status, got: %s", resp.Status)
	}

	// 2. NOW CANCEL THE INITIALIZATION CONTEXT (simulating handleProviderSelection returning)
	initCancel()

	// Give a small delay so any process termination from context cancellation would have occurred
	time.Sleep(100 * time.Millisecond)

	// 3. Send another command with a fresh context (simulating handlePhoneNumberInput)
	newCtx, newCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer newCancel()

	nextResp, err := worker.SendCommand(newCtx, "start_qr", nil)
	if err != nil {
		t.Fatalf("worker failed after init context cancellation: %v", err)
	}
	// Pyrofork worker returns error status for QR, which proves it is ALIVE and responded!
	if nextResp.Status != "error" {
		t.Errorf("expected start_qr error response from live worker, got: %s", nextResp.Status)
	}
	t.Logf("PASS: Worker successfully survived caller context cancellation!")
}

func TestWorkerLargePayloadBuffer(t *testing.T) {
	// Create mock worker process
	w := &WorkerProcess{
		pending:    make(map[string]chan *Response),
		timeout:    5 * time.Second,
		workerType: "test_large",
	}

	// Create a response larger than standard 64KB bufio.MaxScanTokenSize (e.g. 120KB)
	largeString := strings.Repeat("A", 120*1024)
	largeResp := Response{
		ID:     "req-large-1",
		Status: "ok",
		Data: map[string]interface{}{
			"large_data": largeString,
		},
	}
	rawJSON, err := json.Marshal(largeResp)
	if err != nil {
		t.Fatalf("failed to marshal large response: %v", err)
	}

	rawJSONWithNewline := append(rawJSON, '\n')
	reader := bytes.NewReader(rawJSONWithNewline)

	respChan := make(chan *Response, 1)
	w.pending["req-large-1"] = respChan

	// Run readStdoutLoop on the reader
	w.readStdoutLoop(reader)

	select {
	case received := <-respChan:
		if received == nil || received.ID != "req-large-1" {
			t.Fatalf("expected to receive response for req-large-1, got %v", received)
		}
		dataStr, ok := received.Data["large_data"].(string)
		if !ok || len(dataStr) != 120*1024 {
			t.Fatalf("expected data string of length 120KB, got length %d", len(dataStr))
		}
		t.Logf("PASS: Successfully handled large payload (%d bytes) exceeding default 64KB scanner limit without truncation or crash!", len(rawJSON))
	default:
		t.Fatalf("FAILED: readStdoutLoop dropped or crashed on >64KB token")
	}
}

func TestWorkerSendCommandTimeout(t *testing.T) {
	w := &WorkerProcess{
		pending:    make(map[string]chan *Response),
		timeout:    50 * time.Millisecond,
		workerType: "test_timeout",
	}

	// Simulate closed worker or no responder
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Direct send with closed stdin should fail immediately or timeout
	w.closed.Store(true)
	_, err := w.SendCommand(ctx, "test_action", nil)
	if err == nil {
		t.Fatalf("expected error sending to closed worker, got nil")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("expected closed error, got: %v", err)
	}
}
