package main

import "testing"

func TestBM25RanksMatchingChunkFirst(t *testing.T) {
	cs := []chunk{
		{file: "a.md", heading: "Colors", text: "red green blue paint"},
		{file: "b.md", heading: "Sandbox", text: "bubblewrap sandbox namespaces apparmor"},
		{file: "c.md", heading: "Food", text: "bread cheese butter"},
	}
	ix := build(cs, false)
	top := ix.search("apparmor namespaces", false, 3)
	if len(top) == 0 || ix.cs[top[0]].file != "b.md" {
		t.Fatalf("top = %v, want b.md first", top)
	}
	if got := ix.search("zzzz", false, 3); len(got) != 3 {
		t.Fatalf("unmatched query should still return k results, got %d", len(got))
	}
}

func TestTokensFoldAccentsAndStopwords(t *testing.T) {
	got := tokens("Cómo REGISTRO el webhook", true)
	want := []string{"registro", "webhook"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
}

func TestLoadSplitsAtHeadings(t *testing.T) {
	cs := load("../..", []string{"docs/mcp.md"}, 180)
	found := false
	for _, c := range cs {
		if c.heading == "The tools" {
			found = true
		}
	}
	if !found {
		t.Fatal("heading chunk 'The tools' not found in docs/mcp.md")
	}
}
