package workers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"universal-session-generator/internal/security"
)

// WorkerProcess manages a single isolated Python MTProto worker process.
type WorkerProcess struct {
	cmd        *exec.Cmd
	cancelFn   context.CancelFunc
	stdin      io.WriteCloser
	stderr     io.ReadCloser
	writeMu    sync.Mutex
	pendingMu  sync.Mutex
	pending    map[string]chan *Response
	reqSeq     int64
	timeout    time.Duration
	closed     atomic.Bool
	workerType string
	waitDone   chan struct{}
	waitErr    error
	closeOnce  sync.Once
}

// StartWorker launches a new Python worker process and starts listening on its stdout/stderr.
func StartWorker(ctx context.Context, pythonPath, scriptPath, workerType string, timeout time.Duration) (*WorkerProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// A worker process is a stateful subprocess that persists across multiple interactive
	// Telegram message rounds (provider selection -> phone number -> OTP -> 2FA).
	// Its lifetime must NOT be bound to the caller's transient request context, which is canceled
	// as soon as the HTTP/bot handler returns. Instead, we give it its own dedicated cancelable
	// lifecycle context that is terminated only when worker.Close() is called.
	procCtx, procCancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, pythonPath, "-u", scriptPath)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		procCancel()
		return nil, fmt.Errorf("failed to create stdin pipe for %s worker: %w", workerType, err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		procCancel()
		_ = stdin.Close()
		return nil, fmt.Errorf("failed to create stdout pipe for %s worker: %w", workerType, err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		procCancel()
		_ = stdin.Close()
		_ = stdoutPipe.Close()
		return nil, fmt.Errorf("failed to create stderr pipe for %s worker: %w", workerType, err)
	}

	if err := cmd.Start(); err != nil {
		procCancel()
		_ = stdin.Close()
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		return nil, fmt.Errorf("failed to start %s worker process: %w", workerType, err)
	}

	waitDone := make(chan struct{})
	worker := &WorkerProcess{
		cmd:        cmd,
		cancelFn:   procCancel,
		stdin:      stdin,
		stderr:     stderrPipe,
		pending:    make(map[string]chan *Response),
		timeout:    timeout,
		workerType: workerType,
		waitDone:   waitDone,
	}

	// Supervise process exit and reap exit code to prevent zombie processes
	go func() {
		worker.waitErr = cmd.Wait()
		worker.closed.Store(true)
		close(waitDone)
	}()

	// Dedicated background goroutine reading stdout continuously
	go worker.readStdoutLoop(stdoutPipe)

	// Stream worker stderr to slog with redaction
	go worker.streamStderr()

	return worker, nil
}

// readStdoutLoop reads JSON responses from the worker stdout and routes them to waiting callers.
func (w *WorkerProcess) readStdoutLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	// Allow up to 2MB tokens so large string sessions and tracebacks never abort scanning
	scannerBuf := make([]byte, 64*1024)
	scanner.Buffer(scannerBuf, 2*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			slog.Debug(fmt.Sprintf("[%s worker] Non-JSON stdout: %s", w.workerType, security.SanitizeLogText(string(line))))
			continue
		}

		w.pendingMu.Lock()
		ch, exists := w.pending[resp.ID]
		w.pendingMu.Unlock()

		if exists && ch != nil {
			select {
			case ch <- &resp:
			default:
			}
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		slog.Warn(fmt.Sprintf("[%s worker] Stdout scanner error", w.workerType), "error", err)
	}

	// Mark closed and notify all waiting callers that worker exited
	w.closed.Store(true)
	w.pendingMu.Lock()
	for id, ch := range w.pending {
		close(ch)
		delete(w.pending, id)
	}
	w.pendingMu.Unlock()
}

// IsAlive returns true if the worker process is running and not marked closed.
func (w *WorkerProcess) IsAlive() bool {
	if w == nil || w.closed.Load() {
		return false
	}
	if w.cmd == nil || w.cmd.Process == nil {
		return false
	}
	select {
	case <-w.waitDone:
		return false
	default:
		return true
	}
}

// SendCommand sends a structured JSON request to the worker and waits for its corresponding response.
func (w *WorkerProcess) SendCommand(ctx context.Context, action string, payload map[string]interface{}) (*Response, error) {
	if !w.IsAlive() {
		return nil, errors.New("worker process is already closed")
	}

	reqID := fmt.Sprintf("req-%d", atomic.AddInt64(&w.reqSeq, 1))
	req := Request{
		ID:      reqID,
		Action:  action,
		Payload: payload,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize worker request: %w", err)
	}

	respChan := make(chan *Response, 1)

	w.pendingMu.Lock()
	w.pending[reqID] = respChan
	w.pendingMu.Unlock()

	defer func() {
		w.pendingMu.Lock()
		delete(w.pending, reqID)
		w.pendingMu.Unlock()
	}()

	w.writeMu.Lock()
	if !w.IsAlive() {
		w.writeMu.Unlock()
		return nil, errors.New("worker process closed before send")
	}
	_, err = fmt.Fprintf(w.stdin, "%s\n", data)
	w.writeMu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("failed to write to worker stdin: %w", err)
	}

	cmdTimeout := w.timeout
	if dl, ok := ctx.Deadline(); ok {
		remaining := time.Until(dl)
		if remaining < cmdTimeout && remaining > 0 {
			cmdTimeout = remaining
		}
	}

	timer := time.NewTimer(cmdTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("worker command '%s' timed out after %v", action, cmdTimeout)
	case <-w.waitDone:
		return nil, errors.New("worker process terminated unexpectedly")
	case resp, ok := <-respChan:
		if !ok || resp == nil {
			return nil, errors.New("worker process terminated or closed stdout unexpectedly")
		}
		return resp, nil
	}
}

// Close gracefully terminates the worker process and frees all pipes.
func (w *WorkerProcess) Close() error {
	w.closeOnce.Do(func() {
		w.closed.Store(true)

		w.writeMu.Lock()
		if w.stdin != nil {
			// Send cancel action before exiting
			cancelReq, _ := json.Marshal(Request{
				ID:     "exit",
				Action: "cancel",
			})
			_, _ = fmt.Fprintf(w.stdin, "%s\n", cancelReq)
			_ = w.stdin.Close()
		}
		w.writeMu.Unlock()

		select {
		case <-w.waitDone:
		case <-time.After(2 * time.Second):
			if w.cmd != nil && w.cmd.Process != nil {
				_ = w.cmd.Process.Kill()
			}
			if w.cancelFn != nil {
				w.cancelFn()
			}
			<-w.waitDone
		}

		if w.cancelFn != nil {
			w.cancelFn()
		}

		w.pendingMu.Lock()
		for id, ch := range w.pending {
			close(ch)
			delete(w.pending, id)
		}
		w.pendingMu.Unlock()
	})

	return w.waitErr
}

// streamStderr reads stderr lines and forwards them to slog with credential redaction.
func (w *WorkerProcess) streamStderr() {
	scanner := bufio.NewScanner(w.stderr)
	errBuf := make([]byte, 16*1024)
	scanner.Buffer(errBuf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		sanitized := security.SanitizeLogText(line)
		slog.Warn(fmt.Sprintf("[%s worker] %s", w.workerType, sanitized))
	}
}
