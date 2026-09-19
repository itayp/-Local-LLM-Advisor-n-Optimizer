package main

import (
	"testing"

	"advisor/internal/server"
)

// The gate measures a model whose speed the machine decides: the smallest
// of 3 billion parameters or more first, then the smaller ones, largest
// first (llama3.2:1b varied 7% between runs on the M1 Pro while each run
// agreed with itself within 3.4%).
func TestGateOrderPrefersAModelOfThreeBillionOrMore(t *testing.T) {
	m := func(name, size string, bytes uint64) server.InstalledModelInfo {
		return server.InstalledModelInfo{Name: name, ParameterSize: size, SizeBytes: bytes}
	}
	got := gateOrder([]server.InstalledModelInfo{
		m("llama3.2:1b", "1.2B", 1_321_098_329),
		m("qwen3:14b", "14.8B", 9_276_198_565),
		m("llama3.1:8b", "8.0B", 4_920_753_328),
		m("llama3.2:3b", "3.2B", 2_019_393_189),
		m("mystery", "", 700_000_000),
	})
	want := []string{"llama3.2:3b", "llama3.1:8b", "qwen3:14b", "llama3.2:1b", "mystery"}
	for i, w := range want {
		if got[i].Name != w {
			t.Fatalf("order %v, want %v", names(got), want)
		}
	}
}

func names(ms []server.InstalledModelInfo) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}
