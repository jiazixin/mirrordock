package config

import (
	"errors"
	"slices"
	"strings"
)

var NotificationChannels = []string{"wecom", "dingtalk", "telegram", "bark", "webhook"}

// Credentials are authenticated ciphertext, never included in configuration API/snapshots.
type Notification struct {
	Enabled     bool   `yaml:"enabled"`
	OnlyFailure bool   `yaml:"only_failure"`
	Endpoint    string `yaml:"endpoint,omitempty"`
	Token       string `yaml:"token,omitempty"`
	Secret      string `yaml:"secret,omitempty"`
	ChatID      string `yaml:"chat_id,omitempty"`
}

func ValidateNotifications(channels map[string]Notification) error {
	for kind, c := range channels {
		if !slices.Contains(NotificationChannels, kind) {
			return errors.New("不支持的通知渠道")
		}
		for _, value := range []string{c.Endpoint, c.Token, c.Secret} {
			encrypted := strings.HasPrefix(value, "enc:v1:") && len(value) <= 16384
			if value != "" && !encrypted {
				return errors.New("通知凭据必须通过通知设置加密保存")
			}
		}
		fields := ChannelFields{
			Endpoint: c.Endpoint != "",
			Token:    c.Token != "",
			ChatID:   c.ChatID != "",
			Secret:   c.Secret != "",
		}
		if !fields.AllowedFor(kind) {
			return errors.New("通知字段与渠道不匹配")
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
