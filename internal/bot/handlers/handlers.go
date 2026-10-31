package handlers

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"universal-session-generator/internal/auth"
	"universal-session-generator/internal/bot/keyboards"
	"universal-session-generator/internal/broadcast"
	"universal-session-generator/internal/database"
	"universal-session-generator/internal/providers"
	"universal-session-generator/internal/security"
)

// HandlerContext contains all services needed by the bot handlers.
type HandlerContext struct {
	AuthMgr      *auth.Manager
	BroadcastMgr *broadcast.Manager
	DB           *database.Database
	RateLimiter  *security.RateLimiter
}

// StartHandler handles the /start command.
func (h *HandlerContext) StartHandler(b *gotgbot.Bot, ctx *ext.Context) error {
	user := ctx.EffectiveSender.User
	if user == nil {
		return nil
	}

	// Enforce private chat only
	if ctx.EffectiveChat.Type != "private" {
		_, err := b.SendMessage(ctx.EffectiveChat.Id, "⚠️ For security reasons, this bot can only be used in private chats.", nil)
		return err
	}

	// Check rate limit
	if !h.RateLimiter.Allow(user.Id) {
		_, err := b.SendMessage(ctx.EffectiveChat.Id, "⏳ You are sending requests too quickly. Please wait a moment.", nil)
		return err
	}

	// Register user in MongoDB (stores ONLY numeric user_id)
	go func() {
		ctxTimeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.DB.AddUser(ctxTimeout, user.Id); err != nil {
			slog.Warn("Failed to store user_id in MongoDB", "user_id", user.Id, "error", err)
		}
	}()

	// Reset any previous auth state
	_ = h.AuthMgr.Cancel(context.Background(), user.Id)

	welcome := `👋 *Welcome to Universal Telegram MTProto Session Generator!*

This bot enables you to safely generate native, production-ready String Sessions for:
• 🔥 *Pyrogram* (Native String Session)
• ⚡ *Telethon* (Native StringSession)
• 🔷 *Gogram* (Native Base64 Session)

🛡️ *Strict Security Policy:*
• *Zero Credential Storage:* OTP codes, 2FA passwords, phone numbers, and generated sessions are processed purely in memory and immediately discarded.
• *Validation Before Delivery:* Every session is validated via a fresh, isolated client before delivery.
• *Safe Private Delivery:* Delivered exclusively to you in this private chat.

Tap *🚀 Generate Session* below to begin.`

	kb := keyboards.MainMenuKeyboard()
	_, err := b.SendMessage(ctx.EffectiveChat.Id, welcome, &gotgbot.SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: kb,
	})
	return err
}

// HelpHandler displays help and instructions.
func (h *HandlerContext) HelpHandler(b *gotgbot.Bot, ctx *ext.Context) error {
	helpText := `ℹ️ *How to Generate an MTProto Session:*

1. Tap *🚀 Generate Session*.
2. Select your preferred library (*Pyrogram*, *Telethon*, or *Gogram*).
3. Choose authentication method (*Phone Number* or *QR Code* where supported).
4. For Phone Login:
   • Send your phone number with country code (e.g. ` + "`+1234567890`" + `).
   • Enter the verification code received in your official Telegram app or SMS.
   • If 2FA is active, enter your cloud password.
5. For QR Login:
   • Scan the generated QR code in *Telegram Settings > Devices > Link Desktop Device*.
6. The bot verifies the session through a fresh isolated client and delivers your native String Session here.

⚠️ *Security Advice:*
Never share your String Session with untrusted bots or users. It provides full access to your account.`

	kb := keyboards.MainMenuKeyboard()
	_, err := b.SendMessage(ctx.EffectiveChat.Id, helpText, &gotgbot.SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: kb,
	})
	return err
}

// AboutHandler displays information about the application.
func (h *HandlerContext) AboutHandler(b *gotgbot.Bot, ctx *ext.Context) error {
	aboutText := `📖 *About Universal Telegram Session Generator*

• *Core Principle:* One Telegram Bot. Three MTProto Libraries. One Unified Interface.
• *Main Engine:* Go (Golang)
• *Supported Providers:*
  - *Pyrogram:* Native String Session (powered by isolated Pyrofork worker)
  - *Telethon:* Native StringSession (powered by isolated Telethon worker)
  - *Gogram:* Native Base64 Session (pure native Go MTProto client)
• *Database:* MongoDB (only stores numeric Telegram User IDs for owner broadcasts)
• *Hosting:* Docker & Render Background Worker ready

🔒 *Strict Privacy:*
No credentials, passwords, OTPs, or session strings are ever permanently stored on disk or database.`

	kb := keyboards.MainMenuKeyboard()
	_, err := b.SendMessage(ctx.EffectiveChat.Id, aboutText, &gotgbot.SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: kb,
	})
	return err
}

// AbortHandler handles /abort (and /cancel) and abort button presses.
func (h *HandlerContext) AbortHandler(b *gotgbot.Bot, ctx *ext.Context) error {
	user := ctx.EffectiveSender.User
	if user == nil {
		return nil
	}

	_ = h.AuthMgr.Cancel(context.Background(), user.Id)
	_ = h.BroadcastMgr.Cancel(user.Id)

	kb := keyboards.MainMenuKeyboard()
	_, err := b.SendMessage(ctx.EffectiveChat.Id, "🛑 Operation aborted. All temporary session memory and workers have been wiped.", &gotgbot.SendMessageOpts{
		ReplyMarkup: kb,
	})
	return err
}

// CancelHandler maintains backward compatibility by delegating to AbortHandler.
func (h *HandlerContext) CancelHandler(b *gotgbot.Bot, ctx *ext.Context) error {
	return h.AbortHandler(b, ctx)
}

// GenerateSessionPrompt starts provider selection.
func (h *HandlerContext) GenerateSessionPrompt(b *gotgbot.Bot, ctx *ext.Context) error {
	user := ctx.EffectiveSender.User
	if user == nil {
		return nil
	}

	if !h.RateLimiter.Allow(user.Id) {
		_, err := b.SendMessage(ctx.EffectiveChat.Id, "⏳ Rate limit exceeded. Please wait a moment.", nil)
		return err
	}

	if err := h.AuthMgr.StartSession(user.Id); err != nil {
		_, err := b.SendMessage(ctx.EffectiveChat.Id, "Failed to start session. Please try /start again.", nil)
		return err
	}

	prompt := `🎯 *Select MTProto Provider:*

Choose which library you would like to generate a native String Session for:

• 🔥 *Pyrogram:* Compatible with Pyrogram and Pyrofork applications.
• ⚡ *Telethon:* Compatible with Telethon Python userbots and scripts.
• 🔷 *Gogram:* Compatible with Gogram native Go applications.

Please select from the keyboard below:`

	kb := keyboards.ProviderSelectionKeyboard()
	_, err := b.SendMessage(ctx.EffectiveChat.Id, prompt, &gotgbot.SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: kb,
	})
	return err
}

// HandleProviderSelection processes provider selection buttons.
func (h *HandlerContext) HandleProviderSelection(b *gotgbot.Bot, ctx *ext.Context, providerName string) error {
	user := ctx.EffectiveSender.User
	if user == nil {
		return nil
	}

	sess := h.AuthMgr.GetSession(user.Id)
	if sess == nil || sess.State != auth.StateSelectingProvider {
		return h.GenerateSessionPrompt(b, ctx)
	}

	// Instant Provider Selection (<1ms): static capabilities check + background pre-warming
	supportsQR, err := h.AuthMgr.SelectProviderFast(user.Id, providerName)
	if err != nil {
		_, _ = b.SendMessage(ctx.EffectiveChat.Id, fmt.Sprintf("❌ Error selecting %s: %v", providerName, err), nil)
		return nil
	}

	displayName := providers.DisplayName(providerName)

	if supportsQR {
		prompt := fmt.Sprintf(`Selected: *%s*

This provider supports both *Phone Number* and *QR Code* authentication.

How would you like to log in?`, displayName)
		kb := keyboards.AuthMethodKeyboard(true)
		_, err := b.SendMessage(ctx.EffectiveChat.Id, prompt, &gotgbot.SendMessageOpts{
			ParseMode:   "Markdown",
			ReplyMarkup: kb,
		})
		return err
	}

	// Phone login prompt
	prompt := fmt.Sprintf(`Selected: *%s*

Please send your Telegram phone number with international country code (e.g. `+"`+1234567890`"+`).

*(QR Code is not supported by %s; phone authentication will be used).*`, displayName, displayName)
	kb := keyboards.AbortOnlyKeyboard()
	_, err = b.SendMessage(ctx.EffectiveChat.Id, prompt, &gotgbot.SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: kb,
	})
	return err
}

// HandleAuthMethodSelection processes choice between Phone and QR.
func (h *HandlerContext) HandleAuthMethodSelection(b *gotgbot.Bot, ctx *ext.Context, method string) error {
	user := ctx.EffectiveSender.User
	if user == nil {
		return nil
	}

	nextState, err := h.AuthMgr.ChooseAuthMethod(user.Id, method)
	if err != nil {
		_, _ = b.SendMessage(ctx.EffectiveChat.Id, fmt.Sprintf("❌ Error: %v", err), nil)
		return nil
	}

	if nextState == auth.StateAwaitingQRAuthentication {
		return h.triggerQRAuth(b, ctx, user.Id)
	}

	// Awaiting phone number
	kb := keyboards.AbortOnlyKeyboard()
	_, err = b.SendMessage(ctx.EffectiveChat.Id, "📱 Please send your Telegram phone number with international country code (e.g. `+1234567890`):", &gotgbot.SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: kb,
	})
	return err
}

func (h *HandlerContext) triggerQRAuth(b *gotgbot.Bot, ctx *ext.Context, userID int64) error {
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, pngBytes, err := h.AuthMgr.StartQRAuth(ctxTimeout, userID)
	if err != nil {
		_, _ = b.SendMessage(ctx.EffectiveChat.Id, fmt.Sprintf("❌ Failed to generate QR code: %v", err), nil)
		return nil
	}

	if res.Status == "error" {
		_, _ = b.SendMessage(ctx.EffectiveChat.Id, fmt.Sprintf("❌ QR generation error: %s", res.ErrorMessage), nil)
		return nil
	}

	caption := `📷 *Scan this QR Code with your Telegram App:*

1. Open Telegram on your smartphone.
2. Go to *Settings > Devices > Link Desktop Device*.
3. Point your camera at this QR code.

⏳ *Waiting for your authorization...*`

	kb := keyboards.AbortOnlyKeyboard()
	_, err = b.SendPhoto(ctx.EffectiveChat.Id, gotgbot.InputFileByReader("qr.png", bytes.NewReader(pngBytes)), &gotgbot.SendPhotoOpts{
		Caption:     caption,
		ParseMode:   "Markdown",
		ReplyMarkup: kb,
	})
	if err != nil {
		return err
	}

	// Run background waiter for QR login
	go func() {
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 180*time.Second)
		defer waitCancel()

		waitRes, waitErr := h.AuthMgr.WaitForQRLogin(waitCtx, userID)
		if waitErr != nil {
			sess := h.AuthMgr.GetSession(userID)
			if sess == nil || sess.State == auth.StateCancelled || sess.State == auth.StateIdle {
				// User already cancelled; exit quietly
				return
			}
			slog.Warn("QR login error or timeout", "user_id", userID, "error", waitErr)
			_, _ = b.SendMessage(ctx.EffectiveChat.Id, fmt.Sprintf("❌ QR Authentication expired or encountered an issue: %v", waitErr), &gotgbot.SendMessageOpts{
				ReplyMarkup: keyboards.MainMenuKeyboard(),
			})
			h.AuthMgr.Cleanup(userID)
			return
		}

		if waitRes.Status == "error" {
			_, _ = b.SendMessage(ctx.EffectiveChat.Id, fmt.Sprintf("❌ QR Authentication failed: %s", waitRes.ErrorMessage), &gotgbot.SendMessageOpts{
				ReplyMarkup: keyboards.MainMenuKeyboard(),
			})
			h.AuthMgr.Cleanup(userID)
			return
		}

		if waitRes.Status == "need_password" {
			_, _ = b.SendMessage(ctx.EffectiveChat.Id, "🔐 *Two-Step Verification (2FA) is enabled on your account.*\n\nPlease send your cloud password below:", &gotgbot.SendMessageOpts{
				ParseMode:   "Markdown",
				ReplyMarkup: keyboards.AbortOnlyKeyboard(),
			})
			return
		}

		// Authorization confirmed! Generate and deliver session!
		_ = h.finalizeSession(b, ctx.EffectiveChat.Id, userID)
	}()

	return nil
}

// HandleAnyMessage routes any incoming message (text or media).
func (h *HandlerContext) HandleAnyMessage(b *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.EffectiveMessage
	user := ctx.EffectiveSender.User
	if user == nil {
		return nil
	}

	// Check if owner is in broadcast mode (accepts text, photo, video, document)
	if h.BroadcastMgr.IsOwner(user.Id) {
		bState := h.BroadcastMgr.GetState()
		if bState == broadcast.StateAwaitingContent {
			preview := msg.Text
			if preview == "" {
				preview = msg.Caption
			}
			if preview == "" {
				preview = "[Media Content]"
			}
			_ = h.BroadcastMgr.SetPendingMessage(user.Id, msg.Chat.Id, msg.MessageId, preview)
			kb := keyboards.BroadcastConfirmationKeyboard()
			_, err := b.SendMessage(msg.Chat.Id, "📢 *Broadcast Content Received!*\n\nPlease confirm whether to send this broadcast to all registered users.", &gotgbot.SendMessageOpts{
				ParseMode:   "Markdown",
				ReplyMarkup: kb,
			})
			return err
		}
	}

	if msg.Text != "" {
		return h.HandleTextMessage(b, ctx)
	}

	return nil
}

// HandleTextMessage routes arbitrary text input depending on user conversation state.
func (h *HandlerContext) HandleTextMessage(b *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.EffectiveMessage
	user := ctx.EffectiveSender.User
	if user == nil {
		return nil
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return nil
	}

	// Check if owner is in broadcast mode
	if h.BroadcastMgr.IsOwner(user.Id) {
		bState := h.BroadcastMgr.GetState()
		if bState == broadcast.StateAwaitingContent {
			_ = h.BroadcastMgr.SetPendingMessage(user.Id, msg.Chat.Id, msg.MessageId, text)
			kb := keyboards.BroadcastConfirmationKeyboard()
			_, err := b.SendMessage(msg.Chat.Id, "📢 *Broadcast Message Received!*\n\nPlease confirm whether to send this broadcast to all registered users.", &gotgbot.SendMessageOpts{
				ParseMode:   "Markdown",
				ReplyMarkup: kb,
			})
			return err
		}
	}

	// Check if user pressed Abort or Cancel at any point during active operations
	if isAbortButton(text) {
		return h.AbortHandler(b, ctx)
	}

	// Check user auth state
	sess := h.AuthMgr.GetSession(user.Id)
	if sess == nil {
		// If idle, check for menu button text
		return h.handleMenuButtons(b, ctx, text)
	}

	switch sess.State {
	case auth.StateIdle:
		return h.handleMenuButtons(b, ctx, text)

	case auth.StateSelectingProvider:
		if isProviderChoice(text) {
			return h.HandleProviderSelection(b, ctx, text)
		}
		if isBackButton(text) {
			return h.StartHandler(b, ctx)
		}
		return h.GenerateSessionPrompt(b, ctx)

	case auth.StateSelectingAuthMethod:
		if isAuthMethodChoice(text) {
			return h.HandleAuthMethodSelection(b, ctx, text)
		}
		if isBackButton(text) {
			_ = h.AuthMgr.StartSession(user.Id)
			return h.GenerateSessionPrompt(b, ctx)
		}
		return nil

	case auth.StateAwaitingPhoneNumber:
		if isBackButton(text) {
			_ = h.AuthMgr.StartSession(user.Id)
			return h.GenerateSessionPrompt(b, ctx)
		}
		return h.handlePhoneNumberInput(b, ctx, user.Id, text)

	case auth.StateAwaitingOTP:
		return h.handleOTPInput(b, ctx, user.Id, text)

	case auth.StateAwaitingTwoFactorPassword:
		return h.handlePasswordInput(b, ctx, user.Id, text)

	default:
		return h.handleMenuButtons(b, ctx, text)
	}
}

func (h *HandlerContext) handleMenuButtons(b *gotgbot.Bot, ctx *ext.Context, text string) error {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "generate") || strings.Contains(lower, "🚀"):
		return h.GenerateSessionPrompt(b, ctx)
	case strings.Contains(lower, "about") || strings.Contains(lower, "📖"):
		return h.AboutHandler(b, ctx)
	case strings.Contains(lower, "help") || strings.Contains(lower, "ℹ️"):
		return h.HelpHandler(b, ctx)
	case isAbortButton(text):
		return h.AbortHandler(b, ctx)
	case strings.Contains(lower, "confirm broadcast") || strings.Contains(lower, "✅"):
		return h.HandleConfirmBroadcast(b, ctx)
	case strings.Contains(lower, "cancel broadcast"):
		return h.AbortHandler(b, ctx)
	default:
		return nil
	}
}

func (h *HandlerContext) handlePhoneNumberInput(b *gotgbot.Bot, ctx *ext.Context, userID int64, phoneInput string) error {
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	_, _ = b.SendMessage(ctx.EffectiveChat.Id, "⏳ Requesting verification code from Telegram...", nil)

	res, err := h.AuthMgr.StartPhoneAuth(ctxTimeout, userID, phoneInput)
	if err != nil {
		_, _ = b.SendMessage(ctx.EffectiveChat.Id, fmt.Sprintf("❌ Error: %v", err), &gotgbot.SendMessageOpts{
			ReplyMarkup: keyboards.AbortOnlyKeyboard(),
		})
		return nil
	}

	if res.Status == "error" {
		_, _ = b.SendMessage(ctx.EffectiveChat.Id, fmt.Sprintf("❌ %s", res.ErrorMessage), &gotgbot.SendMessageOpts{
			ReplyMarkup: keyboards.AbortOnlyKeyboard(),
		})
		return nil
	}

	prompt := `📩 *Verification Code Sent!*

Please enter the login code you received.
Check your official Telegram app messages (or SMS).

*Tip:* Format like ` + "`12345` or `1 2 3 4 5`" + `.`

	kb := keyboards.AbortOnlyKeyboard()
	_, err = b.SendMessage(ctx.EffectiveChat.Id, prompt, &gotgbot.SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: kb,
	})
	return err
}

func (h *HandlerContext) handleOTPInput(b *gotgbot.Bot, ctx *ext.Context, userID int64, otpInput string) error {
	// Normalize code (remove spaces/hyphens)
	cleanCode := strings.ReplaceAll(strings.ReplaceAll(otpInput, " ", ""), "-", "")

	ctxTimeout, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	res, err := h.AuthMgr.SubmitOTP(ctxTimeout, userID, cleanCode)
	if err != nil {
		_, _ = b.SendMessage(ctx.EffectiveChat.Id, fmt.Sprintf("❌ Error verifying code: %v", err), nil)
		return nil
	}

	if res.Status == "error" {
		_, _ = b.SendMessage(ctx.EffectiveChat.Id, fmt.Sprintf("❌ %s", res.ErrorMessage), nil)
		return nil
	}

	if res.Status == "need_password" {
		prompt := `🔐 *Two-Step Verification (2FA) Required*

Your Telegram account is protected by a 2FA cloud password.
Please enter your 2FA password to complete authentication:

*(Your password will NEVER be saved or logged).*`
		kb := keyboards.AbortOnlyKeyboard()
		_, err := b.SendMessage(ctx.EffectiveChat.Id, prompt, &gotgbot.SendMessageOpts{
			ParseMode:   "Markdown",
			ReplyMarkup: kb,
		})
		return err
	}

	// Signed in successfully! Generate and deliver session!
	return h.finalizeSession(b, ctx.EffectiveChat.Id, userID)
}

func (h *HandlerContext) handlePasswordInput(b *gotgbot.Bot, ctx *ext.Context, userID int64, passwordInput string) error {
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	res, err := h.AuthMgr.SubmitPassword(ctxTimeout, userID, passwordInput)
	if err != nil {
		_, _ = b.SendMessage(ctx.EffectiveChat.Id, fmt.Sprintf("❌ Error verifying 2FA: %v", err), nil)
		return nil
	}

	if res.Status == "error" {
		_, _ = b.SendMessage(ctx.EffectiveChat.Id, fmt.Sprintf("❌ %s", res.ErrorMessage), nil)
		return nil
	}

	// Signed in successfully! Generate and deliver session!
	return h.finalizeSession(b, ctx.EffectiveChat.Id, userID)
}

func (h *HandlerContext) finalizeSession(b *gotgbot.Bot, chatID int64, userID int64) error {
	sess := h.AuthMgr.GetSession(userID)
	providerName := "Selected Provider"
	if sess != nil && sess.ProviderName != "" {
		providerName = sess.ProviderName
	}

	statusMsg, _ := b.SendMessage(chatID, "⚙️ *Generating, validating, and delivering your String Session to Saved Messages...*", &gotgbot.SendMessageOpts{
		ParseMode: "Markdown",
	})

	ctxTimeout, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	account, msgID, err := h.AuthMgr.CompleteAndDeliverToSavedMessages(ctxTimeout, userID)
	if err != nil {
		slog.Error("Session generation/delivery failed", "user_id", userID, "error", err)
		if statusMsg != nil {
			_, _ = statusMsg.Delete(b, nil)
		}
		kb := keyboards.MainMenuKeyboard()
		_, _ = b.SendMessage(chatID, fmt.Sprintf("❌ Session Generation Failed: %v\n\nPlease try again.", err), &gotgbot.SendMessageOpts{
			ReplyMarkup: kb,
		})
		return nil
	}

	if statusMsg != nil {
		_, _ = statusMsg.Delete(b, nil)
	}

	accountName := "Authorized Account"
	usernameStr := "N/A"
	accountIDStr := "N/A"
	if account != nil {
		accountIDStr = fmt.Sprintf("`%d`", account.ID)
		accountName = strings.TrimSpace(account.FirstName + " " + account.LastName)
		if account.Username != "" {
			usernameStr = "@" + account.Username
		}
	}

	deliveryMsg := fmt.Sprintf(`🎉 *Session Successfully Generated & Delivered!*

Your MTProto String Session has been securely generated, verified with an isolated client, and delivered directly to your official Telegram *Saved Messages*.

• *Provider:* %s
• *Account ID:* %s
• *Account Name:* %s
• *Username:* %s
• *Saved Messages Msg ID:* #%d
• *Status:* ✅ Verified & Delivered

🔒 *Security Notice:*
Your String Session grants full account access. Keep your session safe and never share it with anyone.
To view or copy your session, open your *Saved Messages* in Telegram.

*(The session has been completely wiped from bot memory and was never saved to any database or log).*`,
		auth.EscapeMarkdown(providerName),
		accountIDStr,
		auth.EscapeMarkdown(accountName),
		auth.EscapeMarkdown(usernameStr),
		msgID,
	)

	kb := keyboards.MainMenuKeyboard()
	_, err = b.SendMessage(chatID, deliveryMsg, &gotgbot.SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: kb,
	})
	return err
}

// BroadcastHandler handles the /broadcast owner command.
func (h *HandlerContext) BroadcastHandler(b *gotgbot.Bot, ctx *ext.Context) error {
	user := ctx.EffectiveSender.User
	if user == nil || !h.BroadcastMgr.IsOwner(user.Id) {
		_, err := b.SendMessage(ctx.EffectiveChat.Id, "⛔ This command is reserved exclusively for the bot owner.", nil)
		return err
	}

	if err := h.BroadcastMgr.StartBroadcastPrompt(user.Id); err != nil {
		_, err := b.SendMessage(ctx.EffectiveChat.Id, fmt.Sprintf("⚠️ %v", err), nil)
		return err
	}

	kb := keyboards.AbortOnlyKeyboard()
	_, err := b.SendMessage(ctx.EffectiveChat.Id, "📢 *Broadcast Mode Activated*\n\nPlease send the message you wish to broadcast to all registered users (text, photo, video, or document):", &gotgbot.SendMessageOpts{
		ParseMode:   "Markdown",
		ReplyMarkup: kb,
	})
	return err
}

// HandleConfirmBroadcast executes the confirmed broadcast.
func (h *HandlerContext) HandleConfirmBroadcast(b *gotgbot.Bot, ctx *ext.Context) error {
	user := ctx.EffectiveSender.User
	if user == nil || !h.BroadcastMgr.IsOwner(user.Id) {
		return nil
	}

	chatID := ctx.EffectiveChat.Id
	statusMsg, err := b.SendMessage(chatID, "🚀 *Broadcast initiated...*", &gotgbot.SendMessageOpts{
		ParseMode: "Markdown",
	})
	if err != nil {
		return err
	}

	updateFunc := func(progress string) {
		if statusMsg != nil {
			_, _, _ = b.EditMessageText(&gotgbot.EditMessageTextOpts{
				Text:      progress,
				ChatId:    chatID,
				MessageId: statusMsg.MessageId,
				ParseMode: "Markdown",
			})
		}
	}

	completeFunc := func(summary string) {
		kb := keyboards.MainMenuKeyboard()
		_, _ = b.SendMessage(chatID, summary, &gotgbot.SendMessageOpts{
			ParseMode:   "Markdown",
			ReplyMarkup: kb,
		})
	}

	return h.BroadcastMgr.ExecuteBroadcast(context.Background(), b, updateFunc, completeFunc)
}

// StatsHandler displays registered user count to the owner.
func (h *HandlerContext) StatsHandler(b *gotgbot.Bot, ctx *ext.Context) error {
	user := ctx.EffectiveSender.User
	if user == nil || !h.BroadcastMgr.IsOwner(user.Id) {
		_, err := b.SendMessage(ctx.EffectiveChat.Id, "⛔ Unauthorized.", nil)
		return err
	}

	ctxTimeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	total, err := h.DB.GetTotalUsers(ctxTimeout)
	if err != nil {
		_, err := b.SendMessage(ctx.EffectiveChat.Id, fmt.Sprintf("❌ Failed to query database: %v", err), nil)
		return err
	}

	statsText := fmt.Sprintf(`📊 *Bot Statistics*

• *Total Registered Users:* %d
• *Database Status:* Connected & Verified`, total)

	_, err = b.SendMessage(ctx.EffectiveChat.Id, statsText, &gotgbot.SendMessageOpts{
		ParseMode: "Markdown",
	})
	return err
}

func isProviderChoice(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "pyrogram") ||
		strings.Contains(lower, "pyrofork") ||
		strings.Contains(lower, "telethon") ||
		strings.Contains(lower, "gogram")
}

func isAuthMethodChoice(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "phone") || strings.Contains(lower, "qr")
}

func isBackButton(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "back") || strings.Contains(lower, "⬅️")
}

func isAbortButton(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "abort") ||
		strings.Contains(lower, "cancel") ||
		strings.Contains(lower, "🛑") ||
		strings.Contains(lower, "❌")
}
