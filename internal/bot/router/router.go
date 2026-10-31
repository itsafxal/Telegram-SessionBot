package router

import (
	"log/slog"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters/message"
	botHandlers "universal-session-generator/internal/bot/handlers"
)

// SetupRouter initializes dispatcher and registers all command and message handlers.
func SetupRouter(h *botHandlers.HandlerContext) (*ext.Dispatcher, *ext.Updater) {
	dispatcher := ext.NewDispatcher(&ext.DispatcherOpts{
		Error: func(b *gotgbot.Bot, ctx *ext.Context, err error) ext.DispatcherAction {
			slog.Error("Telegram update handling error", "error", err)
			return ext.DispatcherActionNoop
		},
		MaxRoutines: ext.DefaultMaxRoutines,
	})

	updater := ext.NewUpdater(dispatcher, nil)

	// Command handlers
	dispatcher.AddHandler(handlers.NewCommand("start", h.StartHandler))
	dispatcher.AddHandler(handlers.NewCommand("help", h.HelpHandler))
	dispatcher.AddHandler(handlers.NewCommand("about", h.AboutHandler))
	dispatcher.AddHandler(handlers.NewCommand("cancel", h.CancelHandler))
	dispatcher.AddHandler(handlers.NewCommand("broadcast", h.BroadcastHandler))
	dispatcher.AddHandler(handlers.NewCommand("stats", h.StatsHandler))

	// All other messages (reply keyboard clicks, text inputs, photos, documents)
	dispatcher.AddHandler(handlers.NewMessage(message.All, h.HandleAnyMessage))

	return dispatcher, updater
}
