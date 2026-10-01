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
			if value != "" && (!strings.HasPrefix(value, "enc:v1:") || len(value) > 16384) {
				return errors.New("通知凭据必须通过通知设置加密保存")
			}
		}
		if kind != "telegram" && (c.Token != "" || c.ChatID != "") || kind != "dingtalk" && c.Secret != "" || kind == "telegram" && c.Endpoint != "" {
			return errors.New("通知字段与渠道不匹配")
		}
	}
	return nil
}
