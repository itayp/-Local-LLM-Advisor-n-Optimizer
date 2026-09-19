package estimate

import (
	"fmt"
	"strings"
	"testing"

	"advisor/internal/hardware"
)

// gpus.yaml's contract. The parser already enforces, for every row: a source
// that resolves, a date, and bandwidth = data rate × bus width ÷ 8 where the
// row states a memory configuration — so loading it is the first test.
func TestDeviceTableLoads(t *testing.T) {
	d, err := DefaultDevices()
	if err != nil {
		t.Fatal(err)
	}
	f := d.File()
	vendors := map[hardware.Vendor]int{}
	derived := 0
	for _, r := range f.GPUs {
		vendors[r.Vendor]++
		if r.DataRateGbps > 0 {
			derived++
		}
		if r.Vendor != hardware.VendorApple && r.DataRateGbps == 0 && !strings.Contains(strings.ToLower(r.Note), "hbm") {
			t.Errorf("%s: a GDDR part should state data_rate_gbps and bus_width_bits so its bandwidth can be re-derived", r.ID)
		}
	}
	// "seed it with consumer NVIDIA, AMD, Intel Arc and Apple Silicon parts"
	for _, v := range []hardware.Vendor{hardware.VendorNVIDIA, hardware.VendorAMD, hardware.VendorIntel, hardware.VendorApple} {
		if vendors[v] == 0 {
			t.Errorf("no %s rows", v)
		}
	}
	if derived == 0 {
		t.Error("no row states a memory configuration")
	}
}

// Real device names, as internal/hardware's probes produce them, must land
// on the rows they should: the order of the file is part of its meaning.
func TestDeviceNamesLandOnTheirRows(t *testing.T) {
	d, err := DefaultDevices()
	if err != nil {
		t.Fatal(err)
	}
	gibs := func(n float64) uint64 { return uint64(n * gib) }
	cases := []struct {
		vendor hardware.Vendor
		name   string
		vram   uint64
		note   string // Apple: "16-core GPU"
		row    string
		gbs    float64
	}{
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 5070 Ti", gibs(15.9), "", "rtx-5070-ti", 896},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 5070 Ti Laptop GPU", gibs(12), "", "rtx-5070-ti-laptop", 672},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 4060 Laptop GPU", gibs(8), "", "rtx-4060-laptop", 256},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 4060 Ti", gibs(16), "", "rtx-4060-ti", 288},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 4060", gibs(8), "", "rtx-4060", 272},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 4070 Ti SUPER", gibs(16), "", "rtx-4070-ti-super", 672},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 4070 SUPER", gibs(12), "", "rtx-4070-family", 504},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 4080 SUPER", gibs(16), "", "rtx-4080-super", 736},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 4090", gibs(24), "", "rtx-4090", 1008},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 3090", gibs(24), "", "rtx-3090", 936},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 3090 Ti", gibs(24), "", "rtx-3090-ti", 1008},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 3080", gibs(10), "", "rtx-3080-10gb", 760},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 3080", gibs(12), "", "rtx-3080-12gb", 912},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 3060", gibs(12), "", "rtx-3060-12gb", 360},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 3060 Lite Hash Rate", gibs(12), "", "rtx-3060-12gb", 360},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 3050 Mobile", gibs(4), "", "rtx-3050-laptop", 192},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 2060", gibs(6), "", "rtx-2060-12-and-6gb", 336},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 2060 SUPER", gibs(8), "", "rtx-20-256bit", 448},
		{hardware.VendorNVIDIA, "NVIDIA GeForce RTX 2080 Ti", gibs(11), "", "rtx-2080-ti", 616},
		{hardware.VendorNVIDIA, "NVIDIA GeForce GTX 1660 SUPER", gibs(6), "", "gtx-1660-super", 336},
		{hardware.VendorNVIDIA, "NVIDIA GeForce GTX 1080 Ti", gibs(11), "", "gtx-1080-ti", 484},
		{hardware.VendorAMD, "AMD Radeon(TM) RX 7900 XTX", gibs(24), "", "rx-7900-xtx", 960},
		{hardware.VendorAMD, "AMD NITRO+ Radeon RX 7900 XTX Vapor-X", gibs(24), "", "rx-7900-xtx", 960},
		{hardware.VendorAMD, "AMD Radeon RX 7900 XT", gibs(20), "", "rx-7900-xt", 800},
		// Linux without the board's own name: pci.ids lists the whole chip id.
		{hardware.VendorAMD, "AMD Radeon RX 7900 XT/7900 XTX/7900 GRE/7900M", gibs(24), "", "rx-7900-xtx", 960},
		{hardware.VendorAMD, "AMD Radeon RX 7900 XT/7900 XTX/7900 GRE/7900M", gibs(20), "", "rx-7900-xt", 800},
		{hardware.VendorAMD, "AMD Radeon RX 7900 XT/7900 XTX/7900 GRE/7900M", gibs(16), "", "rx-7900-gre", 576},
		{hardware.VendorAMD, "AMD Radeon RX 7700 XT / 7800 XT", gibs(16), "", "rx-7800-xt", 624},
		{hardware.VendorAMD, "AMD Radeon RX 6700/6700 XT/6750 XT / 6800M/6850M XT", gibs(12), "", "rx-6700-xt", 384}, // the slowest of the matches
		{hardware.VendorAMD, "AMD Radeon RX 6700 XT", gibs(12), "", "rx-6700-xt", 384},
		{hardware.VendorAMD, "AMD Radeon RX 9070 XT", gibs(16), "", "rx-9070-xt", 640},
		{hardware.VendorAMD, "AMD FirePro D700", gibs(6), "", "firepro-d700", 264},
		{hardware.VendorIntel, "Intel(R) Arc(TM) A770 Graphics", gibs(15.9), "", "arc-a770", 560},
		{hardware.VendorIntel, "Intel(R) Arc(TM) B580 Graphics", gibs(12), "", "arc-b580", 456},
		{hardware.VendorApple, "Apple M1 Pro", 0, "16-core GPU", "apple-m1-pro", 200},
		{hardware.VendorApple, "Apple M1", 0, "8-core GPU", "apple-m1", 68.25},
		{hardware.VendorApple, "Apple M2", 0, "10-core GPU", "apple-m2", 100},
		{hardware.VendorApple, "Apple M4 Max", 0, "40-core GPU", "apple-m4-max-40", 546},
		{hardware.VendorApple, "Apple M4 Max", 0, "32-core GPU", "apple-m4-max", 410},
		{hardware.VendorApple, "Apple M4 Max", 0, "", "apple-m4-max", 410}, // core count unread: the slower variant
		{hardware.VendorApple, "Apple M3 Max", 0, "40-core GPU", "apple-m3-max-40", 400},
		{hardware.VendorApple, "Apple M2 Ultra", 0, "76-core GPU", "apple-ultra-m1-m2", 800},
		{hardware.VendorApple, "Apple M5 Pro", 0, "20-core GPU", "apple-m5-pro", 307},
	}
	for _, c := range cases {
		g := hardware.GPU{Vendor: c.vendor, Name: c.name, VRAMBytes: c.vram, VRAMKnown: c.vram > 0, Note: c.note}
		spec, ok := d.GPU(g)
		if !ok {
			t.Errorf("%q (%d GiB): not found, want %s", c.name, c.vram/gib, c.row)
			continue
		}
		if spec.RowID != c.row || spec.BandwidthGBs != c.gbs {
			t.Errorf("%q (%d GiB): row %s at %v GB/s, want %s at %v", c.name, c.vram/gib, spec.RowID, spec.BandwidthGBs, c.row, c.gbs)
		}
		if spec.Source == "" {
			t.Errorf("%q: no source", c.name)
		}
	}

	for _, g := range []hardware.GPU{
		{Vendor: hardware.VendorNVIDIA, Name: "NVIDIA graphics card (model unknown: no driver installed)"},
		{Vendor: hardware.VendorIntel, Name: "Intel(R) Iris(R) Xe Graphics"},
		{Vendor: hardware.VendorAMD, Name: "AMD Radeon(TM) Graphics"},
		{Vendor: hardware.VendorAMD, Name: "NVIDIA GeForce RTX 4090"},    // the vendor is a condition too
		{Vendor: hardware.VendorNVIDIA, Name: "NVIDIA GeForce RTX 3080"}, // memory unread: neither 3080 row may claim it
	} {
		if spec, ok := d.GPU(g); ok {
			t.Errorf("%q must not match a row, got %s", g.Name, spec.RowID)
		}
	}
}

func TestProcessorNamesLandOnTheirRows(t *testing.T) {
	d, err := DefaultDevices()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"12th Gen Intel(R) Core(TM) i5-12400F":           "intel-core-12-14-desktop",
		"13th Gen Intel(R) Core(TM) i7-13700H":           "intel-core-12-14-mobile",
		"12th Gen Intel(R) Core(TM) i5-1235U":            "intel-core-12-14-mobile",
		"11th Gen Intel(R) Core(TM) i7-1165G7 @ 2.80GHz": "intel-core-10-11-mobile",
		"Intel(R) Core(TM) i5-10400F CPU @ 2.90GHz":      "intel-core-10-11-desktop",
		"Intel(R) Core(TM) i9-9880H CPU @ 2.30GHz":       "intel-core-6-9-mobile",
		"Intel(R) Core(TM) i7-8700K CPU @ 3.70GHz":       "intel-core-6-9-desktop",
		"Intel(R) Core(TM) Ultra 7 155H":                 "intel-core-ultra-mobile",
		"Intel(R) Core(TM) Ultra 7 258V":                 "intel-lunar-lake",
		"Intel(R) Core(TM) Ultra 9 285K":                 "intel-core-ultra-desktop",
		"AMD Ryzen 7 7840U w/ Radeon 780M Graphics":      "amd-ryzen-mobile-6000-8000",
		"AMD Ryzen 7 5800H with Radeon Graphics":         "amd-ryzen-mobile-4000-5000",
		"AMD Ryzen 9 5950X 16-Core Processor":            "amd-ryzen-am4",
		"AMD Ryzen 7 5800X3D 8-Core Processor":           "amd-ryzen-am4",
		"AMD Ryzen 5 5600X 6-Core Processor":             "amd-ryzen-am4",
		"AMD Ryzen 7 7800X3D 8-Core Processor":           "amd-ryzen-am5",
		"AMD Ryzen 9 7950X 16-Core Processor":            "amd-ryzen-am5",
		"AMD Ryzen AI 9 HX 370 w/ Radeon 890M":           "amd-ryzen-ai-300",
		"AMD Ryzen AI Max+ 395 w/ Radeon 8060S":          "amd-strix-halo",
		"AMD Ryzen Threadripper PRO 5975WX 32-Cores":     "amd-threadripper-pro-wx",
		"Intel(R) Xeon(R) Silver 4314 CPU @ 2.40GHz":     "intel-xeon-scalable-3",
		"Intel(R) Xeon(R) CPU E5-1650 v2 @ 3.50GHz":      "intel-xeon-e5-v2",
		"Intel(R) Xeon(R) CPU E5-2697 v2 @ 2.70GHz":      "intel-xeon-e5-v2",
	}
	for name, want := range cases {
		spec, ok := d.SystemMemory(hardware.CPU{Model: name})
		if !ok || spec.RowID != want {
			t.Errorf("%q: row %q (found %v), want %q", name, spec.RowID, ok, want)
		}
	}
	for _, name := range []string{"unknown", "", "Snapdragon(R) X Elite - X1E80100 - Qualcomm(R) Oryon(TM) CPU"} {
		if spec, ok := d.SystemMemory(hardware.CPU{Model: name}); ok {
			t.Errorf("%q must not match, got %s", name, spec.RowID)
		}
	}
}

func TestParserRejectsWhatItShould(t *testing.T) {
	base := `
sources: {s: "a source"}
gpus:
  - {id: a, vendor: nvidia, name: ['RTX 4090'], bandwidth_gbs: %s, data_rate_gbps: 21, bus_width_bits: 384, source: %s, checked: %s%s}
system_memory:
  - {id: m, cpu: ['Core'], low_gbs: 25.6, high_gbs: 51.2, memory: "DDR4", source: s, checked: 2026-09-19}
`
	build := func(bw, source, checked, extra string) string {
		return fmt.Sprintf(base, bw, source, checked, extra)
	}
	if _, err := ParseDevices([]byte(build("1008", "s", "2026-09-19", ""))); err != nil {
		t.Fatalf("the well-formed file must parse: %v", err)
	}
	bad := map[string]string{
		"bandwidth that is not rate × width ÷ 8": build("1100", "s", "2026-09-19", ""),
		"a source that is not listed":            build("1008", "nowhere", "2026-09-19", ""),
		"a missing date":                         build("1008", "s", "soon", ""),
		"an override without its measurement":    build("1008", "s", "2026-09-19", ", efficiency: [0.4, 0.5]"),
		"an unknown key":                         build("1008", "s", "2026-09-19", ", tflops: 82"),
	}
	for name, doc := range bad {
		if _, err := ParseDevices([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
