package keyboards

import (
	"github.com/PaulSonOfLars/gotgbot/v2"
)

// MainMenuKeyboard returns the primary ReplyKeyboardMarkup for the bot.
func MainMenuKeyboard() gotgbot.ReplyKeyboardMarkup {
	return gotgbot.ReplyKeyboardMarkup{
		Keyboard: [][]gotgbot.KeyboardButton{
			{
				{Text: "🚀 Generate Session"},
			},
			{
				{Text: "ℹ️ Help"},
				{Text: "📖 About"},
			},
			{
				{Text: "❌ Cancel"},
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
				{Text: "❌ Cancel"},
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
		{Text: "❌ Cancel"},
	})

	return gotgbot.ReplyKeyboardMarkup{
		Keyboard:       rows,
		ResizeKeyboard: true,
	}
}

// CancelOnlyKeyboard returns a keyboard with only the Cancel option during input steps.
func CancelOnlyKeyboard() gotgbot.ReplyKeyboardMarkup {
	return gotgbot.ReplyKeyboardMarkup{
		Keyboard: [][]gotgbot.KeyboardButton{
			{
				{Text: "❌ Cancel"},
			},
		},
		ResizeKeyboard: true,
	}
}

// BroadcastConfirmationKeyboard returns confirmation keyboard for broadcasting.
func BroadcastConfirmationKeyboard() gotgbot.ReplyKeyboardMarkup {
	return gotgbot.ReplyKeyboardMarkup{
		Keyboard: [][]gotgbot.KeyboardButton{
			{
				{Text: "✅ Confirm Broadcast"},
				{Text: "❌ Cancel Broadcast"},
			},
		},
		ResizeKeyboard: true,
	}
}
