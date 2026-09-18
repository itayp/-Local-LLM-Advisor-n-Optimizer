package hardware

import (
	"context"
	"strings"
	"testing"
)

func TestHumanGB(t *testing.T) {
	for b, want := range map[uint64]string{
		16303 * mib:     "16 GB",   // a 16 GB card as nvidia-smi reports it
		12713115648:     "11.8 GB", // not rounded up to 12
		8188 * mib:      "8 GB",
		17179869184:     "16 GB",
		16114044 * 1024: "15.4 GB", // a 16 GB Linux machine's MemTotal, honestly
		5726623744:      "5.3 GB",
		512 * mib:       "512 MB",
	} {
		if got := humanGB(b); got != want {
			t.Errorf("humanGB(%d) = %q, want %q", b, got, want)
		}
	}
}

func TestArticle(t *testing.T) {
	for name, want := range map[string]string{"NVIDIA GeForce RTX 3090": "an", "AMD Radeon RX 7900 XTX": "an", "Intel Iris Xe Graphics": "an",
		"Apple M1 Pro": "an", "Qualcomm Adreno": "a", "Radeon": "a"} {
		if got := article(name); got != want {
			t.Errorf("article(%q) = %q", name, got)
		}
	}
}

func TestSizeTierBoundaries(t *testing.T) {
	cases := map[uint64]Tier{
		6 * gib:     TierGPUSmall,  // 6 GB cards, the Mac Pro's D700
		5726623744:  TierGPUSmall,  // 8 GB Macs
		7 * gib:     TierGPUMedium, //
		8188 * mib:  TierGPUMedium, // 8 GB cards
		12713115648: TierGPUMedium, // 16 GB Macs
		12 * gib:    TierGPUMedium,
		16303 * mib: TierGPULarge, // 16 GB cards
		24 * gib:    TierGPULarge,
		32607 * mib: TierGPUXL, // 32 GB cards
		96 * gib:    TierGPUXL,
	}
	for b, want := range cases {
		if got := sizeTier(b); got != want {
			t.Errorf("sizeTier(%s) = %s, want %s", humanGB(b), got, want)
		}
	}
}

func TestOrderGPUsPutsTheAnswerFirst(t *testing.T) {
	gpus := []GPU{
		{Name: "iGPU", IsIntegrated: true, IntegratedKnown: true, ExpectedBackend: PathVulkan, VRAMBytes: 512 * mib, VRAMKnown: true},
		{Name: "unusable dGPU", IntegratedKnown: true, ExpectedBackend: PathNone, VRAMBytes: 24 * gib, VRAMKnown: true},
		{Name: "small dGPU", IntegratedKnown: true, ExpectedBackend: PathCUDA, VRAMBytes: 8 * gib, VRAMKnown: true},
		{Name: "big dGPU", IntegratedKnown: true, ExpectedBackend: PathCUDA, VRAMBytes: 24 * gib, VRAMKnown: true},
		{Name: "unknown", IntegratedKnown: true, ExpectedBackend: PathUnknown},
	}
	orderGPUs(gpus)
	var got []string
	for _, g := range gpus {
		got = append(got, g.Name)
	}
	if strings.Join(got, ",") != "big dGPU,small dGPU,iGPU,unknown,unusable dGPU" {
		t.Fatalf("order: %v", got)
	}
}

func TestFingerprintIdentifiesHardwareNotState(t *testing.T) {
	f := newFakeEnv("windows", "amd64")
	f.home = `C:\Users\itay`
	loadFixture(t, f, "windows/rtx5070ti-desktop")
	base, err := detect(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	fp := Fingerprint(base)
	if !strings.HasPrefix(fp, FingerprintVersion+"-") || len(fp) < 20 {
		t.Fatalf("fingerprint %q", fp)
	}

	same := base
	same.Hostname = "renamed"
	same.OSVersion = "Windows 11 Pro 25H2 (build 26200.1)"
	same.Storage.FreeBytes = 1
	same.RAMBytes += 3 * mib // a firmware update that reserves a little more
	same.GPUs = append([]GPU(nil), base.GPUs...)
	same.GPUs[0].DriverVersion = "615.00"
	same.GPUs[0].VRAMBytes -= 64 * mib // a driver that reserves a little more
	same.GPUs[0], same.GPUs[1] = same.GPUs[1], same.GPUs[0]
	if Fingerprint(same) != fp {
		t.Error("hostname, OS version, drivers, free space, small reserved-memory shifts and order are not new hardware")
	}

	swapped := base
	swapped.GPUs = append([]GPU(nil), base.GPUs...)
	swapped.GPUs[0].PCIID = "10de:2b85" // an RTX 5090
	swapped.GPUs[0].VRAMBytes = 32607 * mib
	if Fingerprint(swapped) == fp {
		t.Error("a different graphics card is a different fingerprint")
	}
	more := base
	more.RAMBytes *= 2
	if Fingerprint(more) == fp {
		t.Error("more memory is a different fingerprint")
	}
}

func TestParentDir(t *testing.T) {
	cases := []struct{ goos, in, want string }{
		{"windows", `C:\Users\itay\.ollama\models`, `C:\Users\itay\.ollama`},
		{"windows", `C:\Users`, `C:\`},
		{"windows", `C:\`, `C:\`},
		{"windows", `D:\models\`, `D:\`},
		{"linux", "/home/itay/.ollama/models", "/home/itay/.ollama"},
		{"linux", "/", "/"},
	}
	for _, c := range cases {
		if got := parentDir(c.in, c.goos); got != c.want {
			t.Errorf("parentDir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStorageReadsTheNearestExistingFolder(t *testing.T) {
	f := newFakeEnv("windows", "amd64")
	f.existing[`C:\Users\itay`] = true
	p := Profile{OS: "windows", Storage: Storage{ModelsDir: `C:\Users\itay\.ollama\models`}}
	detectStorage(context.Background(), f, &p)
	if p.Storage.ModelsDirExists || f.diskPath != `C:\Users\itay` || !p.Storage.FreeKnown {
		t.Fatalf("a folder Ollama has not created yet is measured on its volume: %+v via %q", p.Storage, f.diskPath)
	}

	f.freeErr = errFake("access denied")
	p = Profile{OS: "windows", Storage: Storage{ModelsDir: `D:\models`}}
	detectStorage(context.Background(), f, &p)
	if p.Storage.FreeKnown || p.Storage.FreeBytes != 0 || len(p.Problems) != 1 {
		t.Fatalf("unreadable free space is unknown, with the reason: %+v %v", p.Storage, p.Problems)
	}
}
