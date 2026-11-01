package router

import (
	"log/slog"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters/message"
	botHandlers "universal-session-generator/internal/bot/handlers"
	"universal-session-generator/internal/bot/middleware"
)

// SetupRouter initializes dispatcher and registers all command and message handlers with private-chat security enforcement.
func SetupRouter(h *botHandlers.HandlerContext) (*ext.Dispatcher, *ext.Updater) {
	dispatcher := ext.NewDispatcher(&ext.DispatcherOpts{
		Error: func(b *gotgbot.Bot, ctx *ext.Context, err error) ext.DispatcherAction {
			slog.Error("Telegram update handling error", "error", err)
			return ext.DispatcherActionNoop
		},
		MaxRoutines: ext.DefaultMaxRoutines,
	})

	updater := ext.NewUpdater(dispatcher, nil)

	// Command handlers wrapped with centralized private chat enforcement
	dispatcher.AddHandler(handlers.NewCommand("start", middleware.RequirePrivateChat(h.StartHandler)))
	dispatcher.AddHandler(handlers.NewCommand("help", middleware.RequirePrivateChat(h.HelpHandler)))
	dispatcher.AddHandler(handlers.NewCommand("about", middleware.RequirePrivateChat(h.AboutHandler)))
	dispatcher.AddHandler(handlers.NewCommand("abort", middleware.RequirePrivateChat(h.AbortHandler)))
	dispatcher.AddHandler(handlers.NewCommand("cancel", middleware.RequirePrivateChat(h.AbortHandler)))
	dispatcher.AddHandler(handlers.NewCommand("broadcast", middleware.RequirePrivateChat(h.BroadcastHandler)))
	dispatcher.AddHandler(handlers.NewCommand("stats", middleware.RequirePrivateChat(h.StatsHandler)))

	// All other messages (reply keyboard clicks, text inputs, photos, documents)
	dispatcher.AddHandler(handlers.NewMessage(message.All, middleware.RequirePrivateChat(h.HandleAnyMessage)))

	return dispatcher, updater
}
