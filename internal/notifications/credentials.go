package notifications

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"mirrordock/internal/config"
)

func openKey(dir string, existing bool) (cipher.AEAD, error) {
	file := filepath.Join(dir, "notification.key")
	st, err := os.Lstat(file)
	if err == nil && (!st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0) {
		return nil, errors.New("通知密钥必须是仅服务身份可读写的普通文件（0600）")
	}
	if errors.Is(err, os.ErrNotExist) {
		if existing {
			return nil, errors.New("通知密钥缺失，请从备份恢复 notification.key")
		}
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, errors.New("无法生成通知密钥")
		}
		if err = config.AtomicWrite(file, key, 0600); err != nil {
			return nil, errors.New("无法保存通知密钥")
		}
	} else if err != nil {
		return nil, errors.New("无法读取通知密钥")
	}
	key, err := os.ReadFile(file)
	if err != nil || len(key) != 32 {
		return nil, errors.New("通知密钥无效，请恢复备份")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (s *Service) encrypt(kind, field, value string) string {
	if value == "" {
		return ""
	}
	nonce := make([]byte, s.aead.NonceSize())
	// The crypto/rand Reader is guaranteed to succeed on supported systems.
	_, _ = rand.Read(nonce)
	return "enc:v1:" + base64.RawStdEncoding.EncodeToString(s.aead.Seal(nonce, nonce, []byte(value), []byte(kind+":"+field)))
}
func (s *Service) decrypt(kind, field, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	b, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, "enc:v1:"))
	if err != nil || len(b) < s.aead.NonceSize() {
		return "", errors.New("通知凭据无法解密，请检查密钥备份")
	}
	out, err := s.aead.Open(nil, b[:s.aead.NonceSize()], b[s.aead.NonceSize():], []byte(kind+":"+field))
	if err != nil {
		return "", errors.New("通知凭据无法解密，请检查密钥备份")
	}
	return string(out), nil
}
func (s *Service) plain(kind string, c config.Notification) (config.Notification, error) {
	var err error
	c.Endpoint, err = s.decrypt(kind, "endpoint", c.Endpoint)
	if err != nil {
		return c, err
	}
	c.Token, err = s.decrypt(kind, "token", c.Token)
	if err != nil {
		return c, err
	}
	c.Secret, err = s.decrypt(kind, "secret", c.Secret)
	return c, err
}

var tokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]{20,}$`)

func endpointURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || len(value) > 4096 || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 || u.Hostname() == "" || u.Opaque != "" || u.User != nil || strings.Contains(value, "#") || !slices.Contains([]string{"http", "https"}, u.Scheme) {
		return nil, errors.New("推送地址必须是有效 HTTP/HTTPS URL，不能含用户名密码或 fragment")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("推送地址端口无效")
		}
	}
	return u, nil
}
func Validate(kind string, c config.Notification, complete bool) error {
	if !slices.Contains(config.NotificationChannels, kind) {
		return errors.New("不支持的通知渠道")
	}
	if len(c.ChatID) > 256 || len(c.Token) > 256 || len(c.Secret) > 512 || strings.IndexFunc(c.ChatID+c.Token+c.Secret, unicode.IsControl) >= 0 {
		return errors.New("通知字段格式或长度无效")
	}
	if kind == "telegram" {
		if c.Token != "" && !tokenPattern.MatchString(c.Token) {
			return errors.New("Bot Token 格式无效")
		}
		if complete && (c.Token == "" || strings.TrimSpace(c.ChatID) == "") {
			return errors.New("请填写 Bot Token 和 Chat ID")
		}
		return nil
	}
	if c.Endpoint == "" {
		if complete {
			return errors.New("请填写完整推送地址")
		}
		return nil
	}
	u, err := endpointURL(c.Endpoint)
	if err != nil {
		return err
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return errors.New("推送地址参数无效")
	}
	switch kind {
	case "wecom":
		if u.Scheme != "https" || u.Host != "qyapi.weixin.qq.com" || u.Path != "/cgi-bin/webhook/send" || len(q["key"]) != 1 || strings.TrimSpace(q.Get("key")) == "" || len(q) != 1 {
			return errors.New("请填写企业微信群机器人完整官方 Webhook（含 key）")
		}
	case "dingtalk":
		if u.Scheme != "https" || u.Host != "oapi.dingtalk.com" || u.Path != "/robot/send" || len(q["access_token"]) != 1 || strings.TrimSpace(q.Get("access_token")) == "" || len(q) != 1 {
			return errors.New("请填写钉钉机器人完整官方 Webhook（含 access_token，不含临时签名）")
		}
	case "bark":
		if _, err := barkURL(u); err != nil {
			return err
		}
	}
	return nil
}
func barkURL(u *url.URL) (string, error) {
	p := strings.TrimRight(u.EscapedPath(), "/")
	prefix, encodedKey, ok := strings.CutLast(p, "/")
	key, err := url.PathUnescape(encodedKey)
	decodedPrefix, prefixErr := url.PathUnescape(prefix)
	if !ok || err != nil || prefixErr != nil || key == "" || key == "." || key == ".." || strings.ContainsAny(key, "/\\") || u.RawQuery != "" || u.ForceQuery {
		return "", errors.New("Bark 地址必须以设备 Key 结尾，请删除示例标题和正文")
	}
	for seg := range strings.SplitSeq(decodedPrefix, "/") {
		if seg == "." || seg == ".." {
			return "", errors.New("Bark 路径前缀无效")
		}
	}
	u.Path = decodedPrefix + "/push"
	u.RawPath = prefix + "/push"
	return key, nil
}
