package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunUsageErrors(t *testing.T) {
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
