package main

import (
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
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

func TestConfigPath(t *testing.T) {
	env := func(values map[string]string) func(string) (string, bool) {
		return func(name string) (string, bool) {
			v, ok := values[name]
			return v, ok
		}
	}
	for _, tc := range []struct {
		name string
		flag string
		env  map[string]string
		want string
	}{
		{"default", "", nil, "configs/config.local.yaml"},
		{"environment", "", map[string]string{"CONFIG_FILE": "env.yaml"}, "env.yaml"},
		{"flag beats environment", "flag.yaml", map[string]string{"CONFIG_FILE": "env.yaml"}, "flag.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := options{configFile: tc.flag}.configPath(env(tc.env))
			if err != nil || got != tc.want {
				t.Fatalf("got %v, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := (options{}).configPath(env(map[string]string{"CONFIG_FILE": " "})); err == nil {
		t.Fatal("accepted an empty CONFIG_FILE")
	}
}
