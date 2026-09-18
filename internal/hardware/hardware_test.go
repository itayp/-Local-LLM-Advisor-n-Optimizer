package hardware

import (
	"context"
	"runtime"
	"testing"
)

func TestDetectStubReportsUnknownNotGuesses(t *testing.T) {
	p, err := Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.OS != runtime.GOOS || p.Arch != runtime.GOARCH {
		t.Fatalf("OS/arch must come from the runtime: %+v", p)
	}
	if p.OSVersion != Unknown || p.CPU.Model != Unknown || p.Hostname != Unknown {
		t.Fatalf("stub must say unknown, not guess: %+v", p)
	}
	if p.RAMKnown || p.GPUUsableKnown || p.CPU.VectorKnown || p.LaptopKnown || p.Storage.FreeKnown {
		t.Fatalf("stub must not claim to know anything: %+v", p)
	}
	if p.Tier != TierUnknown || p.Summary == "" {
		t.Fatalf("tier unknown and a sentence for the UI are required: %+v", p)
	}
	if p.GPUs == nil {
		t.Fatal("GPUs must be an empty list, not null, so the UI never special-cases it")
	}
}
