package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceURLsRejectCredentialsWithoutRestrictingAssetURLs(t *testing.T) {
	for _, address := range []string{"https://mirror.example/repo?access_token=SECRET", "https://mirror.example/key?signature=SECRET", "https://user:SECRET@mirror.example/repo", "https://mirror.example/repo#SECRET", "https://mirror.example/repo?"} {
		if err := SourceURL(address); err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatal("credential URL accepted or leaked", err)
		}
	}
	if err := HTTPURL("https://assets.example/file?signature=SECRET"); err != nil {
		t.Fatal("signed asset URL rejected", err)
	}
	if err := SourceURL("https://mirror.example/$releasever/repo"); err != nil {
		t.Fatal(err)
	}
	c := Defaults()
	c.Auth = Auth{Username: "admin", Password: "fixture"}
	c.Path, c.MirrorRoot = "./state", "./mirror"
	c.Sources = []Source{{ID: "repo", Name: "Repo", Type: "reposync", Dest: "repo", Upstreams: map[string]string{"src1": "https://mirror.example/repo"}, RepoSync: RepoSync{RepoID: "repo", Releasever: "9", Basearch: "x86_64", GPGKeys: []string{"https://mirror.example/key"}}}}
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := c.Validate(file); err != nil {
		t.Fatal(err)
	}
	c.Sources[0].RepoSync.GPGKeys[0] += "?token=SECRET"
	if err := c.Validate(file); err == nil {
		t.Fatal("GPG credential accepted")
	}
	c.Sources[0].RepoSync.GPGKeys[0] = "https://mirror.example/key"
	c.Sources[0].Upstreams["src1"] += "?token=SECRET"
	if err := c.Validate(file); err == nil {
		t.Fatal("repo credential accepted")
	}
	c.Sources[0].Type = "github_release"
	c.Sources[0].GitHubRelease.Repository = "o/r"
	if err := c.Validate(file); err == nil {
		t.Fatal("unused credential URL accepted")
	}
}

func TestRsyncModuleAndDirectoryContentsValidation(t *testing.T) {
	c := Defaults()
	c.Auth = Auth{Username: "admin", Password: "fixture"}
	c.Path = "./state"
	c.MirrorRoot = "./mirror"
	c.Sources = []Source{{ID: "repo", Type: "rsync", Name: "Repo", Dest: "nested/api", Upstreams: map[string]string{}}}
	file := filepath.Join(t.TempDir(), "config.yaml")
	for address, want := range map[string]bool{"rsync://host": false, "rsync://host/": false, "rsync://host/module": true, "rsync://host/module/path": false, "rsync://host/module/path/": true} {
		c.Sources[0].Upstreams["src1"] = address
		if err := c.Validate(file); (err == nil) != want {
			t.Fatal(address, err)
		}
	}
}
