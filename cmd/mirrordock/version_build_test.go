//go:build integration

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildVersion(t *testing.T) {
	const (
		commandTimeout = 2 * time.Minute
		binaryTimeout  = 10 * time.Second
		pipeWaitDelay  = 5 * time.Second
	)
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
	// command runs argv[0] with the remaining arguments in dir.
	command := func(t *testing.T, dir string, argv ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), commandTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = dir
		// Descendants retaining output pipes must not outlive the command's wait.
		cmd.WaitDelay = pipeWaitDelay
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%q: %v\n%s", argv, errors.Join(err, ctx.Err()), out)
		}
		return strings.TrimSpace(string(out))
	}
	git := func(args ...string) string {
		t.Helper()
		return command(t, repo, append([]string{"git"}, args...)...)
	}
	git("init", "-q")
	git("config", "user.name", "Version Fixture")
	git("config", "user.email", "version@example.invalid")
	git("config", "commit.gpgsign", "false")
	git("config", "core.hooksPath", os.DevNull)
	git("add", ".")
	git("commit", "-qm", "fixture")
	revision := git("rev-parse", "--short=12", "HEAD")
	// buildCase expects the output to equal prefix when suffix is empty,
	// otherwise to start with prefix and end with suffix.
	type buildCase struct {
		name   string
		dir    string
		prefix string
		suffix string
		flags  []string
	}
	check := func(tc buildCase) {
		t.Helper()
		t.Run(tc.name, func(t *testing.T) {
			binary := filepath.Join(base, "binary")
			argv := append([]string{"go", "build", "-o", binary}, tc.flags...)
			command(t, tc.dir, append(argv, ".")...)
			// No source directory or Git executable is available at runtime.
			ctx, cancel := context.WithTimeout(t.Context(), binaryTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary)
			cmd.Dir = base
			cmd.Env = []string{"PATH=" + filepath.Join(base, "missing")}
			cmd.WaitDelay = pipeWaitDelay
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run binary: %v\n%s", errors.Join(err, ctx.Err()), out)
			}
			got := strings.TrimSpace(string(out))
			if (tc.suffix == "" && got != tc.prefix) || !strings.HasPrefix(got, tc.prefix) || !strings.HasSuffix(got, tc.suffix) {
				t.Fatalf("got %q, want prefix %q and suffix %q", got, tc.prefix, tc.suffix)
			}
			t.Log(got)
		})
	}
	clean := " (revision=" + revision + ")"
	check(buildCase{name: "no tag", dir: repo, prefix: "v0.0.0-", suffix: "-" + revision})
	git("tag", "1.1")
	check(buildCase{name: "legacy tag ignored", dir: repo, prefix: "v0.0.0-", suffix: "-" + revision})
	git("tag", "v1.2.3")
	check(buildCase{name: "standard tag", dir: repo, prefix: "v1.2.3" + clean})
	for _, value := range []string{"", "dev", "(devel)"} {
		check(buildCase{name: "placeholder " + value, dir: repo, prefix: "v1.2.3" + clean, flags: []string{"-ldflags", "-X main.version=" + value}})
	}
	check(buildCase{name: "explicit override", dir: repo, prefix: "custom-build" + clean, flags: []string{"-ldflags", "-X main.version=custom-build"}})
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	modified := " (revision=" + revision + ", modified)"
	check(buildCase{name: "dirty tag", dir: repo, prefix: "v1.2.3+dirty" + modified})
	check(buildCase{name: "dirty override", dir: repo, prefix: "custom-build" + modified, flags: []string{"-ldflags", "-X main.version=custom-build"}})
	git("add", ".")
	git("commit", "-qm", "after tag")
	revision = git("rev-parse", "--short=12", "HEAD")
	check(buildCase{name: "after tag", dir: repo, prefix: "v1.2.4-0.", suffix: "-" + revision})
	check(buildCase{name: "disabled VCS", dir: repo, prefix: "dev", flags: []string{"-buildvcs=false"}})
	check(buildCase{name: "source archive", dir: archive, prefix: "dev"})
	check(buildCase{name: "archive override", dir: archive, prefix: "v1.2.3", flags: []string{"-ldflags", "-X main.version=v1.2.3"}})
}
