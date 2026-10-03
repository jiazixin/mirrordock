package runs

import (
	"context"
	"encoding/json/v2"
	"io"
	"mirrordock/internal/config"
	syncer "mirrordock/internal/sync"
	"strings"
	"testing"
	"time"
)

func TestHistoricalURLMigrationCoversBothFactsAndReadAPI(t *testing.T) {
	runner := runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error { return nil })
	e, cfg, store, m := testEngine(t, "20s", runner)
	e.Close()
	if _, err := store.DB.Exec("DELETE FROM app_state WHERE key='public-source-urls-v1'"); err != nil {
		t.Fatal(err)
	}
	address := "https://mirror.example/repo?access_token=REPOSITORY_SECRET"
	key := "https://user:KEY_SECRET@mirror.example/key?signature=KEY_SECRET"
	var last int64
	for range 105 {
		r := &Run{SourceID: "test", State: "failed", Type: "reposync", ElapsedSeconds: 10, Snapshot: Snapshot{Source: config.Source{Upstreams: map[string]string{"src1": address}, RepoSync: config.RepoSync{GPGKeys: []string{key}}}}, Attempts: []Attempt{{Number: 1, Candidate: address, Error: "failed: " + address}}, Events: []Event{{Message: "尝试来源: " + address}}, Error: "failed: " + key}
		var err error
		last, err = store.CreateRun("test", "failed", time.Now().UTC().Format(time.RFC3339Nano), r)
		if err != nil {
			t.Fatal(err)
		}
	}
	recovered, err := New(cfg, store, m, runner, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	for _, table := range []string{"runs", "run_results"} {
		rows, err := store.ReadDB.Query("SELECT data FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "SECRET") {
				t.Fatal("persisted secret survived", table)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if count != 105 {
			t.Fatal("migration lost records", table, count)
		}
	}
	r, err := recovered.Get(last)
	if err != nil || r.ElapsedSeconds != 10 || r.Snapshot.Source.Upstreams["src1"] != "https://mirror.example/repo" || r.Snapshot.Source.RepoSync.GPGKeys[0] != "https://mirror.example/key" {
		t.Fatal(r, err)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	clean, err := redactHistoricalURLs(data)
	if err != nil || string(clean) != string(data) {
		t.Fatal("migration not idempotent", err)
	}
}

func TestSnapshotSourceDoesNotMutateConfiguration(t *testing.T) {
	s := config.Source{Upstreams: map[string]string{"src1": "https://mirror.example/repo?token=SECRET"}, RepoSync: config.RepoSync{GPGKeys: []string{"https://mirror.example/key?token=SECRET"}}}
	snapshot := publicSource(s)
	data, err := json.Marshal(snapshot)
	if err != nil || strings.Contains(string(data), "SECRET") {
		t.Fatal(string(data), err)
	}
	if !strings.Contains(s.Upstreams["src1"], "SECRET") || !strings.Contains(s.RepoSync.GPGKeys[0], "SECRET") {
		t.Fatal("snapshot changed YAML truth")
	}
}

func TestHistoricalURLRedactionHandlesOverlappingAddresses(t *testing.T) {
	short := "https://mirror.example/repo?token=SECRET"
	long := short + "&signature=OTHER_SECRET"
	r := &Run{Snapshot: Snapshot{Source: config.Source{Upstreams: map[string]string{"src1": short, "src2": long}}}, Events: []Event{{Message: long + " " + short}}}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		clean, err := redactHistoricalURLs(data)
		if err != nil || strings.Contains(string(clean), "SECRET") {
			t.Fatal(string(clean), err)
		}
	}
}

func TestHistoricalURLMigrationFailureIsRetryable(t *testing.T) {
	runner := runnerFunc(func(context.Context, syncer.Task, func(syncer.Progress), io.Writer) error { return nil })
	e, cfg, store, m := testEngine(t, "20s", runner)
	e.Close()
	if _, err := store.DB.Exec("DELETE FROM app_state WHERE key='public-source-urls-v1'"); err != nil {
		t.Fatal(err)
	}
	r := &Run{SourceID: "test", State: "failed", Snapshot: Snapshot{Source: config.Source{Upstreams: map[string]string{"src1": "https://mirror.example/repo?token=SECRET"}}}}
	id, err := store.CreateRun("test", "failed", time.Now().UTC().Format(time.RFC3339Nano), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec("CREATE TRIGGER reject_redaction BEFORE UPDATE ON run_results BEGIN SELECT RAISE(ABORT,'fixture'); END"); err != nil {
		t.Fatal(err)
	}
	if failed, err := New(cfg, store, m, runner, time.Local); err == nil {
		failed.Close()
		t.Fatal("migration failure did not stop startup")
	}
	if marker, err := store.State("public-source-urls-v1"); err != nil || marker != "" {
		t.Fatal("partial migration marked done", marker, err)
	}
	if _, err := store.DB.Exec("DROP TRIGGER reject_redaction"); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(cfg, store, m, runner, time.Local)
	if err != nil {
		t.Fatal("migration did not recover", err)
	}
	defer recovered.Close()
	for _, table := range []string{"runs", "run_results"} {
		var data []byte
		if err := store.ReadDB.QueryRow("SELECT data FROM "+table+" WHERE id=?", id).Scan(&data); err != nil || strings.Contains(string(data), "SECRET") {
			t.Fatal(table, err)
		}
	}
}
