package runs

import (
	"encoding/json/v2"
	"mirrordock/internal/config"
	"strings"
	"testing"
)

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
