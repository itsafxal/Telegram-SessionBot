package workers

import (
	"context"
	"path/filepath"
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
