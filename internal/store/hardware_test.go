package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"advisor/internal/hardware"
)

func sampleProfile(gpuName, pciID string, vram uint64) hardware.Profile {
	return hardware.Profile{
		OS: "windows", OSVersion: "Windows 11 Pro 24H2 (build 26100.4652)", Arch: "amd64", Hostname: "pc",
		CPU:      hardware.CPU{Model: "AMD Ryzen 7 7800X3D 8-Core Processor", CoresPhysical: 8, CoresLogical: 16, HasAVX2: true, VectorKnown: true},
		RAMBytes: 34271797248, RAMKnown: true,
		GPUs: []hardware.GPU{{
			Vendor: hardware.VendorNVIDIA, Name: gpuName, VRAMBytes: vram, VRAMKnown: true, VRAMSource: "nvidia-smi memory.total",
			DriverVersion: "610.62", IntegratedKnown: true, PCIID: pciID, ExpectedBackend: hardware.PathCUDA,
			ExpectedBackendReason: "why", ExpectedBackendRule: "nvidia-cuda",
		}},
		GPUUsableBytes: vram, GPUUsableKnown: true, GPUUsableSource: "the card",
		Storage: hardware.Storage{ModelsDir: `C:\Users\u\.ollama\models`, ModelsDirSource: "default"},
		Tier:    hardware.TierGPULarge, Summary: "A desktop with " + gpuName + ".",
	}
}

func TestHardwareProfilesKeepHistory(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	if _, err := s.LatestHardwareProfile(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty table: %v", err)
	}

	old := sampleProfile("NVIDIA GeForce RTX 3080", "10de:2206", 10240<<20)
	r1, err := s.AddHardwareProfile(ctx, old, "v1")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := s.AddHardwareProfile(ctx, old, "v1") // another start, same hardware
	if err != nil {
		t.Fatal(err)
	}
	swapped := sampleProfile("NVIDIA GeForce RTX 5070 Ti", "10de:2c05", 16303<<20)
	r3, err := s.AddHardwareProfile(ctx, swapped, "v2")
	if err != nil {
		t.Fatal(err)
	}
	if r1.Fingerprint != r2.Fingerprint || r2.Fingerprint == r3.Fingerprint {
		t.Fatalf("fingerprints: %s %s %s", r1.Fingerprint, r2.Fingerprint, r3.Fingerprint)
	}

	// A benchmark that ran on the old card still finds it.
	got, err := s.HardwareProfile(ctx, r1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Profile, old) || got.DaemonVersion != "v1" {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got.Profile, old)
	}
	latest, _ := s.LatestHardwareProfile(ctx)
	if latest.ID != r3.ID {
		t.Fatalf("latest = %d, want %d", latest.ID, r3.ID)
	}
	prev, err := s.HardwareProfileBefore(ctx, r3.ID)
	if err != nil || prev.ID != r2.ID {
		t.Fatalf("previous start: %+v %v", prev, err)
	}
	if _, err := s.HardwareProfileBefore(ctx, r1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the first start has no previous: %v", err)
	}
	if _, err := s.HardwareProfile(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id: %v", err)
	}

	confs, err := s.HardwareConfigurations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(confs) != 2 || confs[0].LatestProfileID != r3.ID || confs[1].Starts != 2 || confs[1].LatestProfileID != r2.ID ||
		confs[1].Latest.GPUs[0].Name != "NVIDIA GeForce RTX 3080" {
		t.Fatalf("configurations: %+v", confs)
	}
}

func TestHardwareUnknownsAreNULL(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	p := hardware.Profile{OS: "linux", OSVersion: hardware.Unknown, Arch: "amd64", Hostname: hardware.Unknown,
		CPU: hardware.CPU{Model: hardware.Unknown}, GPUs: []hardware.GPU{}, Tier: hardware.TierUnknown, Summary: "unknown"}
	r, err := s.AddHardwareProfile(ctx, p, "dev")
	if err != nil {
		t.Fatal(err)
	}
	var avx2, ram, usable, laptop sql.NullInt64
	if err := s.DB().QueryRowContext(ctx, `SELECT cpu_avx2, ram_bytes, gpu_usable_bytes, is_laptop FROM hardware_profiles WHERE id = ?`, r.ID).
		Scan(&avx2, &ram, &usable, &laptop); err != nil {
		t.Fatal(err)
	}
	if avx2.Valid || ram.Valid || usable.Valid || laptop.Valid {
		t.Fatalf("unknown must be NULL, never 0: %v %v %v %v", avx2, ram, usable, laptop)
	}

	known := sampleProfile("NVIDIA GeForce RTX 3090", "10de:2204", 24<<30)
	r, _ = s.AddHardwareProfile(ctx, known, "dev")
	if err := s.DB().QueryRowContext(ctx, `SELECT cpu_avx2, ram_bytes, gpu_usable_bytes FROM hardware_profiles WHERE id = ?`, r.ID).
		Scan(&avx2, &ram, &usable); err != nil {
		t.Fatal(err)
	}
	if avx2.Int64 != 1 || ram.Int64 != 34271797248 || usable.Int64 != 24<<30 {
		t.Fatalf("known values are stored: %v %v %v", avx2, ram, usable)
	}
}

func TestMigrationAddsDaemonVersion(t *testing.T) {
	s := openTemp(t)
	v, err := s.SchemaVersion(context.Background())
	if err != nil || v < 2 {
		t.Fatalf("schema version %d, %v", v, err)
	}
	if _, err := s.DB().Exec(`SELECT daemon_version FROM hardware_profiles`); err != nil {
		t.Fatal(err)
	}
}
