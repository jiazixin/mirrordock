package notification

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/sys/unix"

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
		return nil, errors.New("notification.key must be a regular file readable and writable only by the service user (0o600)")
	}
	if errors.Is(err, os.ErrNotExist) {
		if hasCredentials {
			return nil, errors.New("notification.key is missing; restore it from the backup")
		}
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, errors.New("cannot generate notification.key")
		}
		if err = config.AtomicWrite(file, key, 0o600); err != nil {
			return nil, errors.New("cannot save notification.key")
		}
	} else if err != nil {
		return nil, errors.New("cannot read notification.key")
	}
	opened, err := os.OpenFile(file, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("cannot open notification.key")
	}
	defer opened.Close() // Read-only key; no buffered output to flush.
	info, err = opened.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("notification.key must be a private regular file")
	}
	key, err := io.ReadAll(io.LimitReader(opened, 33))
	if err != nil || len(key) != 32 {
		return nil, errors.New("notification.key is invalid; restore the backup")
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
	failure := ErrCredentialsUnreadable
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
	invalid := fieldError("endpoint", "invalid_url", "must be an HTTP or HTTPS URL without credentials or a fragment")
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
			return nil, fieldError("endpoint", "invalid_port", "has an invalid port")
		}
	}
	return endpoint, nil
}

// officialWebhook reports whether endpoint is the exact HTTPS robot address
// of a vendor and carries exactly one non-empty credential query parameter.
func officialWebhook(params officialWebhookOptions) bool {
	endpoint := params.endpoint
	query := params.query
	host := params.host
	path := params.path
	parameter := params.parameter

	return endpoint.Scheme == "https" &&
		endpoint.Host == host &&
		endpoint.Path == path &&
		len(query) == 1 &&
		len(query[parameter]) == 1 &&
		strings.TrimSpace(query.Get(parameter)) != ""
}

// Validate checks the fields of a channel of kind; complete requires every
// credential to be present.
func Validate(kind string, c config.Notification, complete bool) error {
	if !slices.Contains(config.NotificationChannels, kind) {
		return ErrUnsupportedChannel
	}
	tooLong := len(c.ChatID) > 256 || len(c.Token) > 256 || len(c.Secret) > 512
	if tooLong || strings.IndexFunc(c.ChatID+c.Token+c.Secret, unicode.IsControl) >= 0 {
		return fieldError("", "invalid_text", "a channel field is too long or contains control characters")
	}
	if kind == "telegram" {
		if c.Token != "" && !tokenPattern.MatchString(c.Token) {
			return fieldError("token", "invalid_format", "is not a valid bot token")
		}
		if complete && (c.Token == "" || strings.TrimSpace(c.ChatID) == "") {
			return fieldError("token", "telegram_incomplete", "bot token and chat ID are required")
		}
		return nil
	}
	if c.Endpoint == "" {
		if complete {
			return fieldError("endpoint", "required", "is required")
		}
		return nil
	}
	endpoint, err := endpointURL(c.Endpoint)
	if err != nil {
		return err
	}
	query, err := url.ParseQuery(endpoint.RawQuery)
	if err != nil {
		return fieldError("endpoint", "invalid_query", "has an invalid query string")
	}
	switch kind {
	case "wecom":
		if !officialWebhook(officialWebhookOptions{endpoint: endpoint, query: query, host: "qyapi.weixin.qq.com", path: "/cgi-bin/webhook/send", parameter: "key"}) {
			return fieldError("endpoint", "wecom_webhook_required", "must be the official WeCom robot webhook with key")
		}
	case "dingtalk":
		if !officialWebhook(officialWebhookOptions{endpoint: endpoint, query: query, host: "oapi.dingtalk.com", path: "/robot/send", parameter: "access_token"}) {
			return fieldError("endpoint", "dingtalk_webhook_required", "must be the official DingTalk robot webhook with access_token and no signature")
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
	invalid := fieldError("endpoint", "bark_key_required", "must end with the device key")
	if !found || keyErr != nil || prefixErr != nil || endpoint.RawQuery != "" || endpoint.ForceQuery {
		return "", invalid
	}
	if key == "" || key == "." || key == ".." || strings.ContainsAny(key, "/\\") {
		return "", invalid
	}
	for segment := range strings.SplitSeq(decodedPrefix, "/") {
		if segment == "." || segment == ".." {
			return "", fieldError("endpoint", "invalid_path", "has an invalid path prefix")
		}
	}
	endpoint.Path = decodedPrefix + "/push"
	endpoint.RawPath = prefix + "/push"
	return key, nil
}

// officialWebhookOptions groups the inputs to officialWebhook.
type officialWebhookOptions struct {
	endpoint  *url.URL
	query     url.Values
	host      string
	path      string
	parameter string
}
