package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"mirrordock/internal/config"
)

// Project is the product name shown in notifications.
const Project = "MirrorDock"

// Message is one notification as sent to a channel.
type Message struct {
	Event string `json:"event"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// maxResponseBytes bounds both response headers and bodies of receivers.
const maxResponseBytes = 64 << 10

// newClient connects directly (no environment proxy) and never follows a
// redirect, so a receiver cannot bounce credentials to another host.
func newClient() *http.Client {
	transport := &http.Transport{
		Proxy:                  nil,
		MaxResponseHeaderBytes: maxResponseBytes,
		MaxIdleConns:           5,
		MaxIdleConnsPerHost:    1,
		IdleConnTimeout:        30 * time.Second,
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func send(ctx context.Context, params sendOptions) error {
	client := params.client
	kind := params.kind
	c := params.c
	m := params.m

	endpoint := c.Endpoint
	text := m.Title + "\n" + m.Body
	var payload any
	switch kind {
	case "wecom", "dingtalk":
		payload = map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}
		if kind == "dingtalk" && c.Secret != "" {
			signed, err := dingTalkSigned(endpoint, c.Secret, time.Now())
			if err != nil {
				return err
			}
			endpoint = signed
		}
	case "telegram":
		endpoint = "https://api.telegram.org/bot" + c.Token + "/sendMessage"
		payload = map[string]string{"chat_id": c.ChatID, "text": text}
	case "bark":
		address, err := url.Parse(endpoint)
		if err != nil {
			return deliveryError("invalid_endpoint")
		}
		key, err := barkURL(address)
		if err != nil {
			return err
		}
		endpoint = address.String()
		payload = map[string]any{
			"device_key": key,
			"title":      m.Title,
			"body":       m.Body,
			"group":      Project,
			"isArchive":  "1",
		}
	case "webhook":
		payload = m
	default:
		return ErrUnsupportedChannel
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return deliveryError("encode_failed")
	}
	request, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
	if err != nil {
		return deliveryError("invalid_endpoint")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return deliveryError("interrupted")
		}
		return deliveryError("network_failed")
	}
	defer response.Body.Close() // Reading and status validation determine delivery success.
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return deliveryError("read_failed")
	}
	if len(body) > maxResponseBytes {
		return deliveryError("response_too_large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &DeliveryError{Reason: "http_status", Status: response.StatusCode}
	}
	if kind == "webhook" {
		return nil
	}
	var result struct {
		Errcode *int  `json:"errcode,omitzero"`
		Code    *int  `json:"code,omitzero"`
		OK      *bool `json:"ok,omitzero"`
	}
	if json.Unmarshal(body, &result) != nil {
		return deliveryError("invalid_response")
	}
	switch kind {
	case "wecom", "dingtalk":
		if result.Errcode == nil || *result.Errcode != 0 {
			return deliveryError("robot_rejected")
		}
	case "telegram":
		if result.OK == nil || !*result.OK {
			return deliveryError("telegram_rejected")
		}
	case "bark":
		if result.Code == nil || *result.Code != 200 {
			return deliveryError("bark_rejected")
		}
	}
	return nil
}

// dingTalkSigned appends the HMAC-SHA256 timestamp signature required by a
// DingTalk robot with signing enabled.
func dingTalkSigned(endpoint, secret string, now time.Time) (string, error) {
	address, err := url.Parse(endpoint)
	if err != nil {
		return "", deliveryError("invalid_endpoint")
	}
	stamp := strconv.FormatInt(now.UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(stamp + "\n" + secret)) // hash.Hash.Write cannot fail.
	query := address.Query()
	query.Set("timestamp", stamp)
	query.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	address.RawQuery = query.Encode()
	return address.String(), nil
}

// sendOptions groups the inputs to send.
type sendOptions struct {
	client *http.Client
	kind   string
	c      config.Notification
	m      Message
}
