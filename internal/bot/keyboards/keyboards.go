package keyboards

import (
	"github.com/PaulSonOfLars/gotgbot/v2"
)

// MainMenuKeyboard returns the primary ReplyKeyboardMarkup for the bot with only the 4 required buttons.
func MainMenuKeyboard() gotgbot.ReplyKeyboardMarkup {
	return gotgbot.ReplyKeyboardMarkup{
		Keyboard: [][]gotgbot.KeyboardButton{
			{
				{Text: "🚀 Generate Session"},
			},
			{
				{Text: "📖 About"},
				{Text: "ℹ️ Help"},
			},
			{
				{Text: "🛑 Abort"},
			},
		},
		ResizeKeyboard: true,
	}
}

// ProviderSelectionKeyboard returns the provider selection ReplyKeyboardMarkup.
func ProviderSelectionKeyboard() gotgbot.ReplyKeyboardMarkup {
	return gotgbot.ReplyKeyboardMarkup{
		Keyboard: [][]gotgbot.KeyboardButton{
			{
				{Text: "🔥 Pyrogram"},
				{Text: "⚡ Telethon"},
			},
			{
				{Text: "🔷 Gogram"},
			},
			{
				{Text: "⬅️ Back"},
				{Text: "🛑 Abort"},
			},
		},
		ResizeKeyboard: true,
	}
}

// AuthMethodKeyboard returns the authentication method selection keyboard.
func AuthMethodKeyboard(supportsQR bool) gotgbot.ReplyKeyboardMarkup {
	rows := [][]gotgbot.KeyboardButton{
		{
			{Text: "📱 Phone Number"},
		},
	}
	if supportsQR {
		rows[0] = append(rows[0], gotgbot.KeyboardButton{Text: "📷 QR Code"})
	}
	rows = append(rows, []gotgbot.KeyboardButton{
		{Text: "⬅️ Back"},
		{Text: "🛑 Abort"},
	})

	return gotgbot.ReplyKeyboardMarkup{
		Keyboard:       rows,
		ResizeKeyboard: true,
	}
}

// AbortOnlyKeyboard returns a keyboard with only the Abort option during active authentication steps.
func AbortOnlyKeyboard() gotgbot.ReplyKeyboardMarkup {
	return gotgbot.ReplyKeyboardMarkup{
		Keyboard: [][]gotgbot.KeyboardButton{
			{
				{Text: "🛑 Abort"},
			},
		},
		ResizeKeyboard: true,
	}
}

// CancelOnlyKeyboard is an alias for AbortOnlyKeyboard for backward compatibility.
func CancelOnlyKeyboard() gotgbot.ReplyKeyboardMarkup {
	return AbortOnlyKeyboard()
}

// BroadcastConfirmationKeyboard returns confirmation keyboard for broadcasting.
func BroadcastConfirmationKeyboard() gotgbot.ReplyKeyboardMarkup {
	return gotgbot.ReplyKeyboardMarkup{
		Keyboard: [][]gotgbot.KeyboardButton{
			{
				{Text: "✅ Confirm Broadcast"},
				{Text: "🛑 Abort"},
			},
		},
		ResizeKeyboard: true,
	}
}
