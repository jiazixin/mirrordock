package database

import (
	"database/sql"
	"encoding/json/v2"
	"errors"
	"testing"
	"time"
)

func TestCreationAndIdempotencyCommitTogether(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	now := time.Date(2026, 10, 30, 1, 2, 3, 0, time.UTC)
	identity := RequestIdentity{Key: "fixture-key", Fingerprint: "fixture-body", CreatedAt: now}
	first := map[string]string{"state": "queued", "source_id": "fixture"}
	create := CreateRunOptions{Source: "fixture", State: "queued", Created: now, Value: first, Request: []RequestIdentity{identity}}
	id, err := store.CreateRun(t.Context(), create)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRun(t.Context(), id, "succeeded", map[string]string{"state": "succeeded"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, found, err := store.IdempotentResponse(t.Context(), identity.Key, now.Add(-IdempotencyWindow))
	if err != nil || !found {
		t.Fatalf("got found=%v error=%v, want stored result", found, err)
	}
	var body map[string]string
	if err := json.Unmarshal(saved.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["state"] != "queued" || saved.Fingerprint != identity.Fingerprint {
		t.Fatalf("got %v, want immutable creation response", body)
	}
	if _, err := store.CreateRun(t.Context(), create); err == nil {
		t.Fatal("got success, want duplicate key failure")
	}
	var count int
	if err := store.reader.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM runs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("got %d runs, want 1 after failed keyed creation", count)
	}
	if _, found, err := store.IdempotentResponse(t.Context(), identity.Key, now.Add(time.Millisecond)); err != nil || found {
		t.Fatalf("got found=%v error=%v, want expired", found, err)
	}
	create.Request[0].CreatedAt = now.Add(IdempotencyWindow + time.Millisecond)
	if _, err := store.CreateRun(t.Context(), create); err != nil {
		t.Fatalf("reuse expired key: %v", err)
	}
}

func TestWriteRollsBackCallbackFailure(t *testing.T) {
	t.Parallel()
	store, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	sentinel := errors.New("fixture failure")
	err = store.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), "INSERT INTO app_state(key,value) VALUES(?,?)", "fixture", "not committed"); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("got %v, want callback failure", err)
	}
	value, err := store.State(t.Context(), "fixture")
	if err != nil || value != "" {
		t.Fatalf("got value=%q error=%v, want rolled back", value, err)
	}
}
