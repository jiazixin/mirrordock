package notifications

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"mirrordock/internal/config"
)

const Project = "MirrorDock"

type Message struct {
	Event string `json:"event"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

func newClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, MaxResponseHeaderBytes: 64 << 10, MaxIdleConns: 5, MaxIdleConnsPerHost: 1, IdleConnTimeout: 30 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func send(ctx context.Context, client *http.Client, kind string, c config.Notification, m Message) error {
	endpoint := c.Endpoint
	text := m.Title + "\n" + m.Body
	var payload any
	switch kind {
	case "wecom", "dingtalk":
		payload = map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}
		if kind == "dingtalk" && c.Secret != "" {
			u, err := url.Parse(endpoint)
			if err != nil {
				return errors.New("推送地址无效")
			}
			stamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
			mac := hmac.New(sha256.New, []byte(c.Secret))
			_, _ = mac.Write([]byte(stamp + "\n" + c.Secret))
			q := u.Query()
			q.Set("timestamp", stamp)
			q.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
			u.RawQuery = q.Encode()
			endpoint = u.String()
		}
	case "telegram":
		endpoint = "https://api.telegram.org/bot" + c.Token + "/sendMessage"
		payload = map[string]string{"chat_id": c.ChatID, "text": text}
	case "bark":
		u, err := url.Parse(endpoint)
		if err != nil {
			return errors.New("Bark 地址无效")
		}
		key, err := barkURL(u)
		if err != nil {
			return err
		}
		endpoint = u.String()
		payload = map[string]any{"device_key": key, "title": m.Title, "body": m.Body, "group": Project, "isArchive": "1"}
	case "webhook":
		payload = m
	default:
		return errors.New("不支持的通知渠道")
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return errors.New("通知编码失败")
	}
	request, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
	if err != nil {
		return errors.New("推送地址无效")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("发送已中断")
		}
		return errors.New("通知网络请求失败或超时，请检查网络和配置")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		return errors.New("通知响应读取失败")
	}
	if len(body) > 64<<10 {
		return errors.New("通知响应超过 64 KiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("通知接收端返回 HTTP %d", response.StatusCode)
	}
	if kind == "webhook" {
		return nil
	}
	var result struct {
		Errcode *int  `json:"errcode"`
		Code    *int  `json:"code"`
		OK      *bool `json:"ok"`
	}
	if json.Unmarshal(body, &result) != nil {
		return errors.New("通知接收端响应格式无效")
	}
	switch kind {
	case "wecom", "dingtalk":
		if result.Errcode == nil || *result.Errcode != 0 {
			return errors.New("机器人拒绝通知，请检查凭据及机器人安全规则")
		}
	case "telegram":
		if result.OK == nil || !*result.OK {
			return errors.New("Telegram 拒绝通知，请检查 Token、Chat ID 和机器人权限")
		}
	case "bark":
		if result.Code == nil || *result.Code != 200 {
			return errors.New("Bark 拒绝通知，请检查设备 Key 和服务配置")
		}
	}
	return nil
}
