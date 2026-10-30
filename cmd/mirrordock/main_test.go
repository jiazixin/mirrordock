package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunUsageErrors(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--bogus"}, {"-c"}, {"--config="}, {"config.yaml"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code != exitUsage {
				t.Fatalf("exit %d, want %d", code, exitUsage)
			}
			if stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "mirrordock: ") || !strings.HasSuffix(stderr.String(), "Run 'mirrordock --help' for usage.\n") {
				t.Fatalf("stdout %q, stderr %q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunHelpAndVersionSkipConfiguration(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("CONFIG_FILE", "missing.yaml")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-h"}, usage},
		{[]string{"--help"}, usage},
		{[]string{"-c", "missing.yaml", "--help"}, usage},
		{[]string{"--version"}, "mirrordock " + currentBuild().String() + "\n"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != exitOK {
				t.Fatalf("exit %d, stderr %q", code, stderr.String())
			}
			if stdout.String() != tc.want || stderr.Len() != 0 {
				t.Fatalf("stdout %q, stderr %q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunConfigSelection(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := t.TempDir()
	flagFile, envFile := filepath.Join(dir, "flag.yaml"), filepath.Join(dir, "env.yaml")
	t.Setenv("CONFIG_FILE", envFile)
	for _, tc := range []struct {
		name          string
		args          []string
		want, notWant string
	}{
		{"environment", []string{}, envFile, flagFile},
		{"flag beats environment", []string{"-c", flagFile}, flagFile, envFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != exitFailure {
				t.Fatalf("exit %d, want %d", code, exitFailure)
			}
			got := stderr.String()
			if stdout.Len() != 0 || !strings.HasPrefix(got, "mirrordock: ") || !strings.Contains(got, tc.want) || strings.Contains(got, tc.notWant) {
				t.Fatalf("stdout %q, stderr %q", stdout.String(), got)
			}
		})
	}
}

func TestRunConflictingConfigFiles(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-c", "a.yaml", "-c", "b.yaml"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("exit %d, want %d", code, exitUsage)
	}
	want := "mirrordock: invalid value \"b.yaml\" for flag -c: " +
		"different files given for -c/--config: \"a.yaml\" and \"b.yaml\"\n" +
		"Run 'mirrordock --help' for usage.\n"
	if stdout.Len() != 0 || stderr.String() != want {
		t.Fatalf("stdout %q, stderr %q; want stderr %q", stdout.String(), stderr.String(), want)
	}
}

func TestRunConfigFailures(t *testing.T) {
	workDir := t.TempDir()
	t.Chdir(workDir)
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	for _, tc := range []struct {
		name string
		args []string
		env  string // CONFIG_FILE; "-" leaves it unset
		// stderr must start with prefix and end with suffix.
		prefix, suffix string
	}{
		{"empty CONFIG_FILE", nil, " ", "mirrordock: config: CONFIG_FILE is set but empty\n", "\n"},
		{"missing explicit file", []string{"-c", missing}, "-", "mirrordock: config: open " + missing + ": ", "\n"},
		{"missing CONFIG_FILE", nil, missing, "mirrordock: config: open " + missing + ": ", "\n"},
		{
			"missing default file", nil, "-",
			"mirrordock: config: open " + filepath.Join(workDir, "configs", "config.local.yaml") + ": ",
			"; built-in defaults do not set the required auth.username and auth.password\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env == "-" {
				t.Setenv("CONFIG_FILE", "")
				os.Unsetenv("CONFIG_FILE")
			} else {
				t.Setenv("CONFIG_FILE", tc.env)
			}
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != exitFailure {
				t.Fatalf("exit %d, want %d", code, exitFailure)
			}
			got := stderr.String()
			if stdout.Len() != 0 || !strings.HasPrefix(got, tc.prefix) || !strings.HasSuffix(got, tc.suffix) ||
				strings.Count(got, "\n") != 1 {
				t.Fatalf("stdout %q, stderr %q; want prefix %q and suffix %q", stdout.String(), got, tc.prefix, tc.suffix)
			}
		})
	}
	if entries, err := os.ReadDir(workDir); err != nil || len(entries) != 0 {
		t.Fatalf("configuration failures created %v (%v)", entries, err)
	}
}
