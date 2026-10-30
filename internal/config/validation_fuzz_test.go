package config

import (
	"net/url"
	"testing"
)

func FuzzProxyURL(f *testing.F) {
	for _, seed := range []string{"", "http://user:secret@127.0.0.1:8080", "socks5://proxy.invalid:1080", "file:///etc/passwd", "http://a.invalid/?token=fixture"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 8192 {
			return
		}
		parsed, err := ParseProxyURL(input)
		if err != nil || parsed == nil {
			return
		}
		if parsed.Host == "" {
			t.Fatal("got empty proxy host, want validated host")
		}
		if _, err := url.Parse(parsed.String()); err != nil {
			t.Fatalf("got unparseable validated URL: %v", err)
		}
	})
}
