package app

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"io"
	"mirrordock/internal/runs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLatestSourceStatusAndExpiredEventStream(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	text := "auth:\n  username: admin\n  password: fixture\npath: ./state\nmirror_root: ./mirror\nsources:\n  - id: quiet\n    name: Quiet\n    type: rsync\n    dest: quiet\n    src1: rsync://localhost/repo/\n  - id: noisy\n    name: Noisy\n    type: rsync\n    dest: noisy\n    src1: rsync://localhost/repo/\n"
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	for i := range 126 {
		run := &runs.Run{SourceID: "noisy", State: "succeeded", CreatedAt: time.Now()}
		if i == 0 {
			run.SourceID, run.State = "quiet", "paused"
		}
		if _, err := a.Store.CreateRun(run.SourceID, run.State, run.CreatedAt.Format(time.RFC3339Nano), run); err != nil {
			t.Fatal(err)
		}
	}
	a.auth.mu.Lock()
	a.auth.sessions["fixture"] = session{expires: time.Now().Add(time.Hour)}
	a.auth.mu.Unlock()
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	get := func(path string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), "GET", server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: "mirrordock_session", Value: "fixture"})
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			t.Fatal("unexpected status", path, res.StatusCode)
		}
		return res
	}
	res := get("/api/runs?latest=1")
	var result struct {
		Items []runs.Run `json:"items"`
		Total int        `json:"total"`
	}
	err = json.UnmarshalRead(res.Body, &result)
	res.Body.Close()
	if err != nil || result.Total != 2 || len(result.Items) != 2 || result.Items[1].SourceID != "quiet" || result.Items[1].State != "paused" {
		t.Fatal("quiet source status disappeared", result, err)
	}
	res = get("/api/events")
	defer res.Body.Close()
	reader := bufio.NewReader(res.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\n" {
			break
		}
	}
	a.auth.mu.Lock()
	a.auth.sessions["fixture"] = session{expires: time.Now().Add(-time.Second)}
	a.auth.mu.Unlock()
	rest, err := io.ReadAll(reader)
	if err != nil || len(rest) != 0 {
		t.Fatal("expired session continued receiving events", string(rest), err)
	}
}
