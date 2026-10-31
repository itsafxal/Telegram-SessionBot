package auth

import (
	"fmt"
	"strings"

	"universal-session-generator/internal/providers"
)

const SecurityWarning = "⚠️ Security Notice:\n\"A Telegram String Session is equivalent to account access credentials. Anyone who obtains it may access your Telegram account. Never share it publicly.\""

// FormatSessionMessage formats the validated session text for private chat delivery.
func FormatSessionMessage(providerName string, account *providers.AccountInfo, sessionString string) string {
	var userIdentifier string
	if account != nil {
		name := strings.TrimSpace(account.FirstName + " " + account.LastName)
		if account.Username != "" {
			userIdentifier = fmt.Sprintf("%s (@%s) [ID: %d]", name, account.Username, account.ID)
		} else {
			userIdentifier = fmt.Sprintf("%s [ID: %d]", name, account.ID)
		}
	} else {
		userIdentifier = "Authorized Account"
	}

	return fmt.Sprintf(`🎉 *Session Generated Successfully*

• *Provider:* %s
• *Account:* %s
• *Status:* ✅ Validated & Verified
• *Session Type:* Native String Session

%s

📋 *Your String Session:*
`+"```\n%s\n```"+`

Tap to copy. Store it securely and never share it with anyone.`,
		providerName,
		escapeMarkdown(userIdentifier),
		SecurityWarning,
		sessionString,
	)
}

func escapeMarkdown(s string) string {
	rep := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	)
	return rep.Replace(s)
}
