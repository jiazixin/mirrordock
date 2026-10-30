package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func FuzzDecodeRequest(f *testing.F) {
	for _, seed := range []string{`{"name":"fixture"}`, `null`, `[]`, `{"unexpected":1}`, `{"name":"a","name":"b"}`, `{"name":"fixture"} {}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		if len(body) > maxRequestBytes+1 {
			return
		}
		request := httptest.NewRequest("POST", "/api/fixture", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		var input struct {
			Name string `json:"name"`
		}
		err := decode(httptest.NewRecorder(), request, &input)
		if err == nil && !strings.HasPrefix(strings.TrimSpace(body), "{") {
			t.Fatalf("got success for %q, want object", body)
		}
	})
}
