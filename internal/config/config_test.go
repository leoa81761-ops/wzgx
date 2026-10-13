package config

import "testing"

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
