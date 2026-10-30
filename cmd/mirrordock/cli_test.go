package main

import (
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want options
	}{
		{nil, options{}},
		{[]string{"-c", "a.yaml"}, options{configFile: "a.yaml"}},
		{[]string{"--config", "a.yaml"}, options{configFile: "a.yaml"}},
		{[]string{"-c=a.yaml"}, options{configFile: "a.yaml"}},
		{[]string{"--config=a.yaml"}, options{configFile: "a.yaml"}},
		{[]string{"-c", "a.yaml", "--config", "a.yaml"}, options{configFile: "a.yaml"}},
		{[]string{"-h"}, options{help: true}},
		{[]string{"--help"}, options{help: true}},
		{[]string{"--version"}, options{version: true}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			got, err := parseArgs(tc.args)
			if err != nil || got != tc.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestParseArgsErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-c"}, "flag needs an argument"},
		{[]string{"--config="}, "must not be empty"},
		{[]string{"-c", " "}, "must not be empty"},
		{[]string{"-c", "a.yaml", "--config", "b.yaml"}, "different files"},
		{[]string{"--bogus"}, "not defined: -bogus"},
		{[]string{"-v"}, "not defined: -v"},
		{[]string{"config.yaml"}, `unexpected argument "config.yaml"`},
		{[]string{"--", "config.yaml"}, `unexpected argument "config.yaml"`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			_, err := parseArgs(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestParseArgsConflictMessage(t *testing.T) {
	t.Parallel()
	// The flag package prefixes the value and flag name.
	_, err := parseArgs([]string{"-c", "a.yaml", "--config=b.yaml"})
	want := `invalid value "b.yaml" for flag -config: different files given for -c/--config: "a.yaml" and "b.yaml"`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
}

func TestConfigPath(t *testing.T) {
	t.Parallel()
	env := func(values map[string]string) func(string) (string, bool) {
		return func(name string) (string, bool) {
			v, ok := values[name]
			return v, ok
		}
	}
	for _, tc := range []struct {
		name         string
		flag         string
		env          map[string]string
		want         string
		wantExplicit bool
	}{
		{"default", "", nil, "configs/config.local.yaml", false},
		{"environment", "", map[string]string{"CONFIG_FILE": "env.yaml"}, "env.yaml", true},
		{"flag", "flag.yaml", nil, "flag.yaml", true},
		{"flag beats environment", "flag.yaml", map[string]string{"CONFIG_FILE": "env.yaml"}, "flag.yaml", true},
		{"flag beats empty environment", "flag.yaml", map[string]string{"CONFIG_FILE": ""}, "flag.yaml", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, explicit, err := options{configFile: tc.flag}.configPath(env(tc.env))
			if err != nil || got != tc.want || explicit != tc.wantExplicit {
				t.Fatalf("got %q, %v, %v; want %q, %v", got, explicit, err, tc.want, tc.wantExplicit)
			}
		})
	}
	for _, value := range []string{"", " ", "\t"} {
		_, _, err := (options{}).configPath(env(map[string]string{"CONFIG_FILE": value}))
		if err == nil || err.Error() != "CONFIG_FILE is set but empty" {
			t.Fatalf("CONFIG_FILE=%q: got %v", value, err)
		}
	}
}
