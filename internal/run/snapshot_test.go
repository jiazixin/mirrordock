package run

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"mirrordock/internal/config"
)

func TestSnapshotSourceDoesNotMutateConfiguration(t *testing.T) {
	t.Parallel()
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
