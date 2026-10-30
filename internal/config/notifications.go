package config

import (
	"fmt"
	"slices"
	"strings"
)

// NotificationChannels lists the supported notification channel kinds.
var NotificationChannels = []string{"wecom", "dingtalk", "telegram", "bark", "webhook"}

// Notification configures one notification channel. Credentials are
// authenticated ciphertext, never included in configuration API/snapshots.
type Notification struct {
	Enabled     bool   `yaml:"enabled"`
	OnlyFailure bool   `yaml:"only_failure"`
	Endpoint    string `yaml:"endpoint,omitempty"`
	Token       string `yaml:"token,omitempty"`
	Secret      string `yaml:"secret,omitempty"`
	ChatID      string `yaml:"chat_id,omitempty"`
}

// ValidateNotifications checks the kind and fields of every notification
// channel.
func ValidateNotifications(channels map[string]Notification) error {
	for kind, c := range channels {
		if !slices.Contains(NotificationChannels, kind) {
			return fieldError("settings.notifications", "unsupported_channel",
				fmt.Sprintf("unsupported channel %q; use wecom, dingtalk, telegram, bark or webhook", kind))
		}
		p := "settings.notifications." + kind
		for _, value := range []string{c.Endpoint, c.Token, c.Secret} {
			encrypted := strings.HasPrefix(value, "enc:v1:") && len(value) <= 16384
			if value != "" && !encrypted {
				return fieldError(p, "plaintext_credentials", "credentials must be saved encrypted through the notification settings")
			}
		}
		fields := ChannelFields{
			Endpoint: c.Endpoint != "",
			Token:    c.Token != "",
			ChatID:   c.ChatID != "",
			Secret:   c.Secret != "",
		}
		if !fields.AllowedFor(kind) {
			return fieldError(p, "channel_fields_mismatch", "fields do not match the channel")
		}
	}
	return nil
}

// ChannelFields records which credential fields a channel update carries.
type ChannelFields struct {
	Endpoint, Token, ChatID, Secret bool
}

// AllowedFor reports whether every present field belongs to kind: Telegram
// uses a bot token and chat ID instead of an endpoint, and only DingTalk
// signs requests with a secret.
func (f ChannelFields) AllowedFor(kind string) bool {
	if kind == "telegram" {
		return !f.Endpoint && !f.Secret
	}
	if f.Token || f.ChatID {
		return false
	}
	return kind == "dingtalk" || !f.Secret
}
