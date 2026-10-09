package main

import "testing"

func TestParseArgs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want cliOptions
	}{
		{nil, cliOptions{ConfigPath: "config.yaml"}},
		{[]string{"-f", "a.yaml", "--config=a.yaml"}, cliOptions{ConfigPath: "a.yaml"}},
		{[]string{"--version"}, cliOptions{ConfigPath: "config.yaml", Version: true}},
		{[]string{"--help"}, cliOptions{ConfigPath: "config.yaml", Help: true}},
		{[]string{"-h"}, cliOptions{ConfigPath: "config.yaml", Help: true}},
	} {
		got, err := parseArgs(tc.args)
		if err != nil || *got != tc.want {
			t.Fatalf("parseArgs(%v) = %v, %v; want %v", tc.args, got, err, tc.want)
		}
	}
	for _, args := range [][]string{{"--config="}, {"-f", ""}, {"--config"}, {"-f", "a", "--config", "b"}, {"position"}, {"--unknown"}, {"--version", "position"}} {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("应拒绝 %v", args)
		}
	}
}
