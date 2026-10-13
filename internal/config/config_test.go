package config

import "testing"

func TestSearxngStaysOffUntilEnabled(t *testing.T) {
	t.Setenv("SEARXNG_ENABLED", "")
	t.Setenv("SEARXNG_URL", "")
	off := Load()
	if off.SearxngEnabled || off.ActiveSearxngURL() != "" {
		t.Fatal(off.SearxngEnabled, off.ActiveSearxngURL())
	}
	if off.SearxngURL != "http://searxng:8080" {
		t.Fatal(off.SearxngURL)
	}
	t.Setenv("SEARXNG_ENABLED", "1")
	on := Load()
	if !on.SearxngEnabled || on.ActiveSearxngURL() != "http://searxng:8080" {
		t.Fatal(on.SearxngEnabled, on.ActiveSearxngURL())
	}
	t.Setenv("SEARXNG_ENABLED", "0")
	if Load().SearxngEnabled {
		t.Fatal("explicit off")
	}
}

func TestSearxngURLDefaultsToTheDockerService(t *testing.T) {
	t.Setenv("SEARXNG_URL", "")
	if got := Load().SearxngURL; got != "http://searxng:8080" {
		t.Fatal(got)
	}
	t.Setenv("SEARXNG_ENGINES", "")
	if got := Load().SearxngEngines; got != "sogou,bing,360search,sogou wechat" {
		t.Fatal(got)
	}
	t.Setenv("SEARXNG_ENGINES", "sogou,bing")
	if got := Load().SearxngEngines; got != "sogou,bing" {
		t.Fatal(got)
	}
	t.Setenv("SEARXNG_URL", "http://127.0.0.1:8888")
	if got := Load().SearxngURL; got != "http://127.0.0.1:8888" {
		t.Fatal(got)
	}
}
