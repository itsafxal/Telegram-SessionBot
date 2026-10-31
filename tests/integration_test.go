package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"universal-session-generator/internal/workers"
)

func findPython() string {
	if p := os.Getenv("PYTHON"); p != "" {
		return p
	}
	return "python"
}

func TestPyroforkWorkerIPC(t *testing.T) {
	scriptPath := filepath.Join("..", "workers", "pyrofork", "worker.py")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("Pyrofork script not found: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	worker, err := workers.StartWorker(ctx, findPython(), scriptPath, "pyrofork_test", 5*time.Second)
	if err != nil {
		t.Fatalf("failed to start pyrofork worker: %v", err)
	}
	defer worker.Close()

	// Test init command
	resp, err := worker.SendCommand(ctx, "init", map[string]interface{}{
		"api_id":   12345,
		"api_hash": "mock_hash_for_test",
	})
	if err != nil {
		t.Fatalf("error sending init command to worker: %v", err)
	}
	// Worker responds with ok or connection attempt error, but structured JSON must succeed
	if resp.ID == "" {
		t.Errorf("worker returned empty request ID")
	}

	// Test QR unsupported check
	qrResp, err := worker.SendCommand(ctx, "start_qr", nil)
	if err != nil {
		t.Fatalf("error sending start_qr command to worker: %v", err)
	}
	if qrResp.Status != "error" {
		t.Errorf("expected Pyrofork to return error for start_qr, got %s", qrResp.Status)
	}

	// Test clean cancellation
	cancelResp, err := worker.SendCommand(ctx, "cancel", nil)
	if err != nil {
		t.Fatalf("error sending cancel to worker: %v", err)
	}
	if cancelResp.Status != "ok" {
		t.Errorf("expected ok status for cancel, got %s", cancelResp.Status)
	}
}

func TestTelethonWorkerIPC(t *testing.T) {
	scriptPath := filepath.Join("..", "workers", "telethon", "worker.py")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("Telethon script not found: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	worker, err := workers.StartWorker(ctx, findPython(), scriptPath, "telethon_test", 5*time.Second)
	if err != nil {
		t.Fatalf("failed to start telethon worker: %v", err)
	}
	defer worker.Close()

	// Test init command
	resp, err := worker.SendCommand(ctx, "init", map[string]interface{}{
		"api_id":   12345,
		"api_hash": "mock_hash_for_test",
	})
	if err != nil {
		t.Fatalf("error sending init command to worker: %v", err)
	}
	if resp.ID == "" {
		t.Errorf("worker returned empty request ID")
	}

	// Test clean cancellation
	cancelResp, err := worker.SendCommand(ctx, "cancel", nil)
	if err != nil {
		t.Fatalf("error sending cancel to worker: %v", err)
	}
	if cancelResp.Status != "ok" {
		t.Errorf("expected ok status for cancel, got %s", cancelResp.Status)
	}
}
