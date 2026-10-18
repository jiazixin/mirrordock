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

// encryptedPrefix marks AES-256-GCM ciphertext stored in YAML.
const encryptedPrefix = "enc:v1:"

// openKey loads the 32-byte AES key from <dir>/notification.key. A key is
// generated only when YAML holds no ciphertext that would need the old one.
func openKey(dir string, hasCredentials bool) (cipher.AEAD, error) {
	file := filepath.Join(dir, "notification.key")
	info, err := os.Lstat(file)
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("通知密钥必须是仅服务身份可读写的普通文件（0600）")
	}
	if errors.Is(err, os.ErrNotExist) {
		if hasCredentials {
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

// associatedData binds ciphertext to its channel and field, so a value
// copied into another field or channel fails authentication.
func associatedData(kind, field string) []byte { return []byte(kind + ":" + field) }

func (s *Service) encrypt(kind, field, value string) string {
	if value == "" {
		return ""
	}
	nonce := make([]byte, s.aead.NonceSize())
	// The crypto/rand Reader is guaranteed to succeed on supported systems.
	_, _ = rand.Read(nonce)
	sealed := s.aead.Seal(nonce, nonce, []byte(value), associatedData(kind, field))
	return encryptedPrefix + base64.RawStdEncoding.EncodeToString(sealed)
}

func (s *Service) decrypt(kind, field, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	failure := errors.New("通知凭据无法解密，请检查密钥备份")
	sealed, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, encryptedPrefix))
	nonceSize := s.aead.NonceSize()
	if err != nil || len(sealed) < nonceSize {
		return "", failure
	}
	plain, err := s.aead.Open(nil, sealed[:nonceSize], sealed[nonceSize:], associatedData(kind, field))
	if err != nil {
		return "", failure
	}
	return string(plain), nil
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
	invalid := errors.New("推送地址必须是有效 HTTP/HTTPS URL，不能含用户名密码或 fragment")
	printable := strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
	if len(value) > 4096 || !printable || strings.Contains(value, "#") {
		return nil, invalid
	}
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Hostname() == "" || endpoint.Opaque != "" || endpoint.User != nil {
		return nil, invalid
	}
	if !slices.Contains([]string{"http", "https"}, endpoint.Scheme) {
		return nil, invalid
	}
	if port := endpoint.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, errors.New("推送地址端口无效")
		}
	}
	return endpoint, nil
}

// officialWebhook reports whether endpoint is the exact HTTPS robot address
// of a vendor and carries exactly one non-empty credential query parameter.
func officialWebhook(endpoint *url.URL, query url.Values, host, path, parameter string) bool {
	return endpoint.Scheme == "https" &&
		endpoint.Host == host &&
		endpoint.Path == path &&
		len(query) == 1 &&
		len(query[parameter]) == 1 &&
		strings.TrimSpace(query.Get(parameter)) != ""
}

func Validate(kind string, c config.Notification, complete bool) error {
	if !slices.Contains(config.NotificationChannels, kind) {
		return errors.New("不支持的通知渠道")
	}
	tooLong := len(c.ChatID) > 256 || len(c.Token) > 256 || len(c.Secret) > 512
	if tooLong || strings.IndexFunc(c.ChatID+c.Token+c.Secret, unicode.IsControl) >= 0 {
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
	endpoint, err := endpointURL(c.Endpoint)
	if err != nil {
		return err
	}
	query, err := url.ParseQuery(endpoint.RawQuery)
	if err != nil {
		return errors.New("推送地址参数无效")
	}
	switch kind {
	case "wecom":
		if !officialWebhook(endpoint, query, "qyapi.weixin.qq.com", "/cgi-bin/webhook/send", "key") {
			return errors.New("请填写企业微信群机器人完整官方 Webhook（含 key）")
		}
	case "dingtalk":
		if !officialWebhook(endpoint, query, "oapi.dingtalk.com", "/robot/send", "access_token") {
			return errors.New("请填写钉钉机器人完整官方 Webhook（含 access_token，不含临时签名）")
		}
	case "bark":
		if _, err := barkURL(endpoint); err != nil {
			return err
		}
	}
	return nil
}

// barkURL extracts the device key from the last path segment of a Bark
// address and rewrites endpoint to the server's JSON /push path.
func barkURL(endpoint *url.URL) (string, error) {
	escapedPath := strings.TrimRight(endpoint.EscapedPath(), "/")
	prefix, encodedKey, found := strings.CutLast(escapedPath, "/")
	key, keyErr := url.PathUnescape(encodedKey)
	decodedPrefix, prefixErr := url.PathUnescape(prefix)
	invalid := errors.New("Bark 地址必须以设备 Key 结尾，请删除示例标题和正文")
	if !found || keyErr != nil || prefixErr != nil || endpoint.RawQuery != "" || endpoint.ForceQuery {
		return "", invalid
	}
	if key == "" || key == "." || key == ".." || strings.ContainsAny(key, "/\\") {
		return "", invalid
	}
	for segment := range strings.SplitSeq(decodedPrefix, "/") {
		if segment == "." || segment == ".." {
			return "", errors.New("Bark 路径前缀无效")
		}
	}
	endpoint.Path = decodedPrefix + "/push"
	endpoint.RawPath = prefix + "/push"
	return key, nil
}
