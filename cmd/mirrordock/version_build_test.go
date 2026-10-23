//go:build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildVersion(t *testing.T) {
	// Keep all commits, tags, binaries and source archives outside the project.
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	archive := filepath.Join(base, "archive")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	code, err := os.ReadFile("version.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{repo, archive} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string][]byte{
			"go.mod":     []byte("module fixture\n\ngo 1.24\n"),
			"version.go": code,
			"main.go":    []byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println(currentBuild()) }\n"),
		} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	command := func(t *testing.T, dir, name string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), name, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %q: %v\n%s", name, args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git := func(args ...string) string {
		t.Helper()
		return command(t, repo, "git", args...)
	}
	git("init", "-q")
	git("config", "user.name", "Version Fixture")
	git("config", "user.email", "version@example.invalid")
	git("config", "commit.gpgsign", "false")
	git("config", "core.hooksPath", os.DevNull)
	git("add", ".")
	git("commit", "-qm", "fixture")
	revision := git("rev-parse", "--short=12", "HEAD")
	check := func(name, dir, prefix, suffix string, flags ...string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			binary := filepath.Join(base, "binary")
			args := append([]string{"build", "-o", binary}, flags...)
			command(t, dir, "go", append(args, ".")...)
			// No source directory or Git executable is available at runtime.
			cmd := exec.CommandContext(t.Context(), binary)
			cmd.Dir = base
			cmd.Env = []string{"PATH=" + filepath.Join(base, "missing")}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run binary: %v\n%s", err, out)
			}
			got := strings.TrimSpace(string(out))
			if (suffix == "" && got != prefix) || !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
				t.Fatalf("got %q; want prefix %q and suffix %q", got, prefix, suffix)
			}
			t.Log(got)
		})
	}
	clean := " (revision=" + revision + ")"
	check("no tag", repo, "v0.0.0-", "-"+revision)
	git("tag", "1.1")
	check("legacy tag ignored", repo, "v0.0.0-", "-"+revision)
	git("tag", "v1.2.3")
	check("standard tag", repo, "v1.2.3"+clean, "")
	for _, value := range []string{"", "dev", "(devel)"} {
		check("placeholder "+value, repo, "v1.2.3"+clean, "", "-ldflags", "-X main.version="+value)
	}
	check("explicit override", repo, "custom-build"+clean, "", "-ldflags", "-X main.version=custom-build")
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	modified := " (revision=" + revision + ", modified)"
	check("dirty tag", repo, "v1.2.3+dirty"+modified, "")
	check("dirty override", repo, "custom-build"+modified, "", "-ldflags", "-X main.version=custom-build")
	git("add", ".")
	git("commit", "-qm", "after tag")
	revision = git("rev-parse", "--short=12", "HEAD")
	check("after tag", repo, "v1.2.4-0.", "-"+revision)
	check("disabled VCS", repo, "dev", "dev", "-buildvcs=false")
	check("source archive", archive, "dev", "dev")
	check("archive override", archive, "v1.2.3", "v1.2.3", "-ldflags", "-X main.version=v1.2.3")
}
