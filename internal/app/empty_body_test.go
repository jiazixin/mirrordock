package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCommandBodyRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"absent", "", 204}, {"object", "{}", 204}, {"unknown", `{"unexpected":true}`, 400}, {"null", "null", 400}, {"array", "[]", 400}, {"trailing", "{} {}", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest("POST", "/api/fixture", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			emptyBody(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })(response, request)
			if response.Code != tc.want {
				t.Fatalf("got %d, want %d: %s", response.Code, tc.want, response.Body.String())
			}
		})
	}
}
