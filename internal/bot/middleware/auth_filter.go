package middleware

import (
	"errors"
	"log/slog"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
)

var (
	ErrNonPrivateChat = errors.New("interaction rejected: not a private chat")
	ErrNilUser        = errors.New("interaction rejected: sender user is missing")
	ErrChatMismatch   = errors.New("interaction rejected: chat ID and user ID mismatch")
)

// ValidatePrivateChat verifies that the incoming update originates strictly from a private 1-on-1 chat
// and that the effective sender user ID matches the effective chat ID.
func ValidatePrivateChat(ctx *ext.Context) error {
	if ctx == nil {
		return errors.New("context is nil")
	}

	if ctx.EffectiveChat == nil {
		return errors.New("effective chat is nil")
	}

	if ctx.EffectiveChat.Type != "private" {
		return ErrNonPrivateChat
	}

	if ctx.EffectiveSender.User == nil || ctx.EffectiveSender.User.Id == 0 {
		return ErrNilUser
	}

	// In Telegram 1-on-1 private chats, EffectiveChat.Id is always identical to the User.Id
	if ctx.EffectiveChat.Id != ctx.EffectiveSender.User.Id {
		return ErrChatMismatch
	}

	return nil
}

// RequirePrivateChat wraps a handler function and enforces strict private chat validation before invocation.
func RequirePrivateChat(fn func(b *gotgbot.Bot, ctx *ext.Context) error) func(b *gotgbot.Bot, ctx *ext.Context) error {
	return func(b *gotgbot.Bot, ctx *ext.Context) error {
		if err := ValidatePrivateChat(ctx); err != nil {
			if ctx.EffectiveChat != nil && ctx.EffectiveChat.Type != "private" {
				slog.Warn("Rejecting non-private chat interaction",
					"chat_id", ctx.EffectiveChat.Id,
					"chat_type", ctx.EffectiveChat.Type,
					"error", err)
				_, _ = b.SendMessage(ctx.EffectiveChat.Id, "⚠️ For security reasons, authentication and session generation are strictly permitted only in private 1-on-1 chats with the bot.", nil)
			}
			return nil
		}
		return fn(b, ctx)
	}
}
