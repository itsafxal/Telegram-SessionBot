package broadcast

import (
	"testing"
)

func TestOwnerAuthorization(t *testing.T) {
	ownerID := int64(123456789)
	mgr := NewManager(ownerID, nil)

	if !mgr.IsOwner(ownerID) {
		t.Fatalf("expected IsOwner to return true for owner ID")
	}

	nonOwnerID := int64(987654321)
	if mgr.IsOwner(nonOwnerID) {
		t.Fatalf("expected IsOwner to return false for non-owner ID")
	}

	// Unauthorized attempt to start broadcast prompt should fail
	if err := mgr.StartBroadcastPrompt(nonOwnerID); err == nil {
		t.Fatalf("expected unauthorized error for non-owner StartBroadcastPrompt, got nil")
	}

	// Authorized attempt should succeed and transition state
	if err := mgr.StartBroadcastPrompt(ownerID); err != nil {
		t.Fatalf("unexpected error on owner StartBroadcastPrompt: %v", err)
	}

	if mgr.GetState() != StateAwaitingContent {
		t.Fatalf("expected state StateAwaitingContent, got %s", mgr.GetState())
	}

	// Setting pending message should transition to StateAwaitingConfirmation
	if err := mgr.SetPendingMessage(ownerID, 111, 222, "Test broadcast"); err != nil {
		t.Fatalf("unexpected error setting pending message: %v", err)
	}

	if mgr.GetState() != StateAwaitingConfirmation {
		t.Fatalf("expected state StateAwaitingConfirmation, got %s", mgr.GetState())
	}

	// Cancel should return state to Idle
	if err := mgr.Cancel(ownerID); err != nil {
		t.Fatalf("unexpected error cancelling broadcast: %v", err)
	}

	if mgr.GetState() != StateIdle {
		t.Fatalf("expected state StateIdle after cancel, got %s", mgr.GetState())
	}
}
