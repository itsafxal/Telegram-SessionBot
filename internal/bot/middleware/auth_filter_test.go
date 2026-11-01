package middleware

import (
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
)

func TestValidatePrivateChat(t *testing.T) {
	// 1. Valid private chat
	validCtx := &ext.Context{
		EffectiveChat: &gotgbot.Chat{
			Id:   123456,
			Type: "private",
		},
		EffectiveSender: &gotgbot.Sender{
			User: &gotgbot.User{
				Id: 123456,
			},
		},
	}
	if err := ValidatePrivateChat(validCtx); err != nil {
		t.Errorf("expected valid private chat to pass, got: %v", err)
	}

	// 2. Group chat
	groupCtx := &ext.Context{
		EffectiveChat: &gotgbot.Chat{
			Id:   -100123456,
			Type: "group",
		},
		EffectiveSender: &gotgbot.Sender{
			User: &gotgbot.User{
				Id: 123456,
			},
		},
	}
	if err := ValidatePrivateChat(groupCtx); err != ErrNonPrivateChat {
		t.Errorf("expected ErrNonPrivateChat for group, got: %v", err)
	}

	// 3. Supergroup chat
	supergroupCtx := &ext.Context{
		EffectiveChat: &gotgbot.Chat{
			Id:   -100987654,
			Type: "supergroup",
		},
		EffectiveSender: &gotgbot.Sender{
			User: &gotgbot.User{
				Id: 123456,
			},
		},
	}
	if err := ValidatePrivateChat(supergroupCtx); err != ErrNonPrivateChat {
		t.Errorf("expected ErrNonPrivateChat for supergroup, got: %v", err)
	}

	// 4. Channel
	channelCtx := &ext.Context{
		EffectiveChat: &gotgbot.Chat{
			Id:   -100555555,
			Type: "channel",
		},
		EffectiveSender: &gotgbot.Sender{
			User: &gotgbot.User{
				Id: 123456,
			},
		},
	}
	if err := ValidatePrivateChat(channelCtx); err != ErrNonPrivateChat {
		t.Errorf("expected ErrNonPrivateChat for channel, got: %v", err)
	}

	// 5. User ID mismatch
	mismatchCtx := &ext.Context{
		EffectiveChat: &gotgbot.Chat{
			Id:   123456,
			Type: "private",
		},
		EffectiveSender: &gotgbot.Sender{
			User: &gotgbot.User{
				Id: 999999, // Mismatched user id
			},
		},
	}
	if err := ValidatePrivateChat(mismatchCtx); err != ErrChatMismatch {
		t.Errorf("expected ErrChatMismatch, got: %v", err)
	}

	// 6. Nil user
	nilUserCtx := &ext.Context{
		EffectiveChat: &gotgbot.Chat{
			Id:   123456,
			Type: "private",
		},
		EffectiveSender: &gotgbot.Sender{
			User: nil,
		},
	}
	if err := ValidatePrivateChat(nilUserCtx); err != ErrNilUser {
		t.Errorf("expected ErrNilUser, got: %v", err)
	}
}
