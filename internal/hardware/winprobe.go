package hardware

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// winProbeScript is the one PowerShell query Windows detection runs. It
// reads CIM (Win32_OperatingSystem, Win32_ComputerSystem, Win32_Processor,
// Win32_SystemEnclosure, Win32_VideoController) and, for each video
// controller that is present, its display-class registry key:
//
//	HKLM\SYSTEM\CurrentControlSet\Enum\<PNPDeviceID>  value Driver = "{4d36e968-…}\0001"
//	HKLM\SYSTEM\CurrentControlSet\Control\Class\{4d36e968-…}\0001
//	    HardwareInformation.qwMemorySize   REG_QWORD, the adapter's own memory
//	    DriverDesc, ProviderName, RadeonSoftwareVersion
//
// Starting from Win32_VideoController rather than walking the class key
// (probe0 walked it) matters: the class key keeps entries for cards that
// were removed, and a user who swapped a GPU must not see the old one.
// qwMemorySize is used, never Win32_VideoController.AdapterRAM, which is a
// 32-bit field and wraps at 4 GB (probe0's finding).
//
// Every value is emitted as a string (the parser owns the types), the output
// is one line of JSON, and nothing in it uses a double quote so the script
// survives any quoting (it is passed with -EncodedCommand anyway).
const winProbeScript = `
$ErrorActionPreference = 'SilentlyContinue'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false
function AsText($v) { if ($null -eq $v) { return '' }; return [string]$v }
function AsU64($v) {
  try {
    if ($null -eq $v) { return '' }
    if ($v -is [byte[]]) {
      if ($v.Length -ge 8) { return [string][BitConverter]::ToUInt64($v, 0) }
      return ''
    }
    return [string]([uint64]$v)
  } catch { return '' }
}
$os = Get-CimInstance Win32_OperatingSystem
$cs = Get-CimInstance Win32_ComputerSystem
$cv = Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion'
$cpus = @(Get-CimInstance Win32_Processor | ForEach-Object {
  [PSCustomObject]@{ name = AsText $_.Name; cores = AsText $_.NumberOfCores; logical = AsText $_.NumberOfLogicalProcessors; architecture = AsText $_.Architecture }
})
$chassis = @(Get-CimInstance Win32_SystemEnclosure | ForEach-Object { $_.ChassisTypes } | ForEach-Object { AsText $_ })
$adapters = @(Get-CimInstance Win32_VideoController | ForEach-Object {
  $vc = $_
  $pnp = AsText $vc.PNPDeviceID
  $key = ''
  if ($pnp -ne '') { $key = AsText (Get-ItemProperty -LiteralPath ('HKLM:\SYSTEM\CurrentControlSet\Enum\' + $pnp) -Name Driver).Driver }
  $p = $null
  if ($key -ne '') { $p = Get-ItemProperty -LiteralPath ('HKLM:\SYSTEM\CurrentControlSet\Control\Class\' + $key) }
  $qw = ''
  if ($null -ne $p) { $qw = AsU64 $p.'HardwareInformation.qwMemorySize' }
  [PSCustomObject]@{
    name = AsText $vc.Name
    pnp_device_id = $pnp
    adapter_compatibility = AsText $vc.AdapterCompatibility
    driver_version = AsText $vc.DriverVersion
    error_code = AsText $vc.ConfigManagerErrorCode
    class_key = $key
    driver_desc = AsText $p.DriverDesc
    provider = AsText $p.ProviderName
    qw_memory_size = $qw
    radeon_software_version = AsText $p.RadeonSoftwareVersion
  }
})
[PSCustomObject]@{
  schema = '1'
  os_caption = AsText $os.Caption
  os_version = AsText $os.Version
  os_build = AsText $os.BuildNumber
  display_version = AsText $cv.DisplayVersion
  ubr = AsText $cv.UBR
  ram_bytes = AsText $cs.TotalPhysicalMemory
  pc_system_type = AsText $cs.PCSystemType
  chassis_types = $chassis
  cpus = $cpus
  adapters = $adapters
  ollama_models_user = AsText ([Environment]::GetEnvironmentVariable('OLLAMA_MODELS', 'User'))
  ollama_models_machine = AsText ([Environment]::GetEnvironmentVariable('OLLAMA_MODELS', 'Machine'))
} | ConvertTo-Json -Depth 6 -Compress
`

// encodedCommand is winProbeScript as -EncodedCommand wants it: base64 of
// UTF-16LE. It sidesteps every quoting rule between Go, CreateProcess and
// PowerShell.
func encodedCommand(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i] = byte(c)
		b[2*i+1] = byte(c >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// winProbe is the script's output.
type winProbe struct {
	Schema              string                `json:"schema"`
	OSCaption           string                `json:"os_caption"`
	OSVersion           string                `json:"os_version"`
	OSBuild             string                `json:"os_build"`
	DisplayVersion      string                `json:"display_version"`
	UBR                 string                `json:"ubr"`
	RAMBytes            string                `json:"ram_bytes"`
	PCSystemType        string                `json:"pc_system_type"`
	ChassisTypes        oneOrMany[string]     `json:"chassis_types"`
	CPUs                oneOrMany[winCPU]     `json:"cpus"`
	Adapters            oneOrMany[winAdapter] `json:"adapters"`
	OllamaModelsUser    string                `json:"ollama_models_user"`
	OllamaModelsMachine string                `json:"ollama_models_machine"`
}

type winCPU struct {
	Name         string `json:"name"`
	Cores        string `json:"cores"`
	Logical      string `json:"logical"`
	Architecture string `json:"architecture"` // Win32_Processor.Architecture: 9 x64, 12 ARM64
}

type winAdapter struct {
	Name                  string `json:"name"`
	PNPDeviceID           string `json:"pnp_device_id"`
	AdapterCompatibility  string `json:"adapter_compatibility"`
	DriverVersion         string `json:"driver_version"`
	ErrorCode             string `json:"error_code"`
	ClassKey              string `json:"class_key"`
	DriverDesc            string `json:"driver_desc"`
	Provider              string `json:"provider"`
	QwMemorySize          string `json:"qw_memory_size"`
	RadeonSoftwareVersion string `json:"radeon_software_version"`
}

// oneOrMany accepts a JSON array or a single value: Windows PowerShell 5.1's
// ConvertTo-Json has a history of unrolling one-element arrays.
type oneOrMany[T any] []T

func (o *oneOrMany[T]) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == "" {
		*o = nil
		return nil
	}
	if strings.HasPrefix(s, "[") {
		var many []T
		if err := json.Unmarshal(b, &many); err != nil {
			return err
		}
		*o = many
		return nil
	}
	var one T
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*o = []T{one}
	return nil
}

func parseWinProbe(out string) (winProbe, error) {
	var w winProbe
	s := strings.TrimSpace(strings.TrimPrefix(out, "\ufeff"))
	if i := strings.IndexByte(s, '{'); i > 0 {
		s = s[i:] // anything PowerShell printed before the JSON (a profile, a warning)
	}
	if err := json.Unmarshal([]byte(s), &w); err != nil {
		return w, fmt.Errorf("PowerShell output is not the expected JSON: %w", err)
	}
	return w, nil
}

// powerShellCandidates is where Windows PowerShell 5.1 always lives, then
// whatever PATH offers. The absolute path first: PATH is the user's to change.
func powerShellCandidates(e env) []string {
	var out []string
	if root := e.getenv("SystemRoot"); root != "" {
		out = append(out, root+`\System32\WindowsPowerShell\v1.0\powershell.exe`)
	}
	return append(out, "powershell.exe", "pwsh.exe")
}

func nvidiaSMICandidatesWindows(e env) []string {
	var out []string
	if root := e.getenv("SystemRoot"); root != "" {
		out = append(out, root+`\System32\nvidia-smi.exe`) // drivers from 2019 on
	}
	if pf := e.getenv("ProgramFiles"); pf != "" {
		out = append(out, pf+`\NVIDIA Corporation\NVSMI\nvidia-smi.exe`) // older drivers
	}
	return append(out, "nvidia-smi.exe")
}

// firstRunnable returns the first candidate that exists or is on PATH.
func firstRunnable(e env, candidates []string) (string, bool) {
	for _, c := range candidates {
		if strings.ContainsAny(c, `\/`) {
			if e.exists(c) {
				return c, true
			}
			continue
		}
		if p, err := e.lookPath(c); err == nil {
			return p, true
		}
	}
	return "", false
}

func detectWindows(ctx context.Context, e env, p *Profile) {
	type smiResult struct {
		gpus []GPU
		ok   bool
		err  error
		path string
	}
	smiCh := make(chan smiResult, 1)
	go func() {
		path, found := firstRunnable(e, nvidiaSMICandidatesWindows(e))
		if !found {
			smiCh <- smiResult{}
			return
		}
		gpus, ok, err := queryNvidiaSMI(ctx, e, path)
		smiCh <- smiResult{gpus, ok, err, path}
	}()

	var w winProbe
	probed := false
	if ps, found := firstRunnable(e, powerShellCandidates(e)); !found {
		p.problem("Windows PowerShell was not found, so the operating system, memory and graphics could not be read")
	} else {
		out, err := e.run(ctx, timeoutPowerShell, ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encodedCommand(winProbeScript))
		if err != nil && strings.TrimSpace(out) == "" {
			p.problem("the Windows system query failed: %v", err)
		} else if w, err = parseWinProbe(out); err != nil {
			p.problem("%v", err)
		} else {
			probed = true
		}
	}
	smi := <-smiCh
	if smi.path != "" && !smi.ok {
		p.problem("nvidia-smi did not answer: %v", smi.err)
	}
	if probed {
		applyWinProbe(p, w)
	}
	// Windows always lists at least one video controller (Basic Display at
	// worst), so an empty list means the CIM query failed, not "no GPU".
	p.gpuListRead = (probed && len(w.Adapters) > 0) || smi.ok
	p.GPUs = mergeWindowsGPUs(p, w.Adapters, smi.gpus, smi.path != "")
	p.Storage.ModelsDir, p.Storage.ModelsDirSource = windowsModelsDir(e, w)
}

// applyWinProbe fills the OS, processor, memory and form factor.
func applyWinProbe(p *Profile, w winProbe) {
	if c := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(w.OSCaption), "Microsoft ")); c != "" {
		v := c
		if w.DisplayVersion != "" {
			v += " " + w.DisplayVersion
		}
		if w.OSBuild != "" {
			b := w.OSBuild
			if w.UBR != "" {
				b += "." + w.UBR
			}
			v += " (build " + b + ")"
		}
		p.OSVersion = v
	}
	if b, err := strconv.Atoi(strings.TrimSpace(w.OSBuild)); err == nil {
		p.windowsBuild = b
	}

	if len(w.CPUs) > 0 {
		if n := cleanName(w.CPUs[0].Name); n != "" {
			p.CPU.Model = n
		}
		cores, logical, okC, okL := 0, 0, true, true
		for _, c := range w.CPUs {
			n, err := strconv.Atoi(strings.TrimSpace(c.Cores))
			okC = okC && err == nil && n > 0
			cores += n
			l, err := strconv.Atoi(strings.TrimSpace(c.Logical))
			okL = okL && err == nil && l > 0
			logical += l
		}
		if okC {
			p.CPU.CoresPhysical = cores
		}
		if okL {
			p.CPU.CoresLogical = logical
		}
		// An ARM64 Windows machine running this x64 build under emulation:
		// describe the machine, not the emulator.
		if strings.TrimSpace(w.CPUs[0].Architecture) == "12" && p.Arch != "arm64" {
			p.Arch = "arm64"
			p.CPU.VectorKnown = true
			p.note("This is an ARM computer running the x64 version of the advisor under emulation.")
		}
	}
	if n, err := strconv.ParseUint(strings.TrimSpace(w.RAMBytes), 10, 64); err == nil && n > 0 {
		p.RAMBytes, p.RAMKnown = n, true
	}

	// Form factor: Win32_ComputerSystem.PCSystemType is Windows' own answer
	// (from the firmware's power profile); the SMBIOS chassis type is the
	// fallback when it is unspecified.
	switch strings.TrimSpace(w.PCSystemType) {
	case "2", "8": // Mobile, Slate
		p.IsLaptop, p.LaptopKnown = true, true
	case "1", "3", "4", "5", "7": // Desktop, Workstation, servers
		p.IsLaptop, p.LaptopKnown = false, true
	default:
		for _, c := range w.ChassisTypes {
			if n, err := strconv.Atoi(strings.TrimSpace(c)); err == nil {
				if laptop, known := laptopFromChassis(n); known {
					p.IsLaptop, p.LaptopKnown = laptop, true
					break
				}
			}
		}
	}
}

var (
	pciVenDevRE = regexp.MustCompile(`(?i)VEN_([0-9A-F]{4})&DEV_([0-9A-F]{4})`)
	// Display heads that are not GPUs: remote-desktop sinks, indirect display
	// drivers, virtual monitors, and the software renderer. None can run a
	// model, and listing them makes a machine look like it has six graphics
	// cards (probe0's list).
	virtualAdapterRE = regexp.MustCompile(`(?i)(remote display|remote desktop|basic render|virtual display|virtual monitor|virtual adapter|indirect display|\bidd\b|iddcx|sudomaker|parsec|splashtop|spacedesk|duet display|citrix|vmware svga|virtualbox|hyper-v video|teamviewer|usb display|displaylink|meta virtual)`)
	basicDisplayRE   = regexp.MustCompile(`(?i)microsoft basic display`)
)

// windowsAdapterGPU turns one present video controller into a GPU, or
// returns filtered=true with a name for FilteredAdapters.
func windowsAdapterGPU(a winAdapter) (g GPU, filtered bool) {
	name := cleanName(a.Name)
	if name == "" {
		name = cleanName(a.DriverDesc)
	}
	pnp := strings.ToUpper(strings.TrimSpace(a.PNPDeviceID))
	bus, _, _ := strings.Cut(pnp, `\`)

	vendor := VendorUnknown
	pciID := ""
	if m := pciVenDevRE.FindStringSubmatch(pnp); m != nil {
		pciID = strings.ToLower(m[1] + ":" + m[2])
		vendor = vendorFromPCI(m[1])
	}
	if vendor == VendorUnknown {
		vendor = vendorFromName(name + " " + a.AdapterCompatibility + " " + a.Provider)
	}
	if bus == "ACPI" && vendor == VendorUnknown && strings.Contains(pnp, "QCOM") {
		vendor = VendorQualcomm
	}

	switch {
	case virtualAdapterRE.MatchString(name) || virtualAdapterRE.MatchString(a.DriverDesc):
		return g, true
	case bus != "PCI" && bus != "ACPI":
		return g, true // ROOT\, SWD\, USB\: software and virtual display devices
	case vendor == VendorUnknown:
		return g, true // a PCI display device from a vendor that makes no GPUs for models (server BMCs, VMs)
	}

	g = GPU{
		Vendor:          vendor,
		Name:            name,
		DriverVersion:   Unknown,
		PCIID:           pciID,
		ExpectedBackend: PathUnknown,
		VRAMSource:      "the registry has no HardwareInformation.qwMemorySize for this adapter",
	}
	if n, err := strconv.ParseUint(strings.TrimSpace(a.QwMemorySize), 10, 64); err == nil && n > 0 {
		g.VRAMBytes, g.VRAMKnown = n, true
		g.VRAMSource = "Windows registry HardwareInformation.qwMemorySize"
	}
	switch vendor {
	case VendorAMD:
		if v := strings.TrimSpace(a.RadeonSoftwareVersion); v != "" {
			g.DriverVersion = v // "25.8.1", the number AMD's own software shows
		} else if v := strings.TrimSpace(a.DriverVersion); v != "" {
			g.DriverVersion = v
		}
	case VendorNVIDIA:
		if v := nvidiaDriverFromWindows(a.DriverVersion); v != "" {
			g.DriverVersion = v
		}
	default:
		if v := strings.TrimSpace(a.DriverVersion); v != "" {
			g.DriverVersion = v
		}
	}
	code, _ := strconv.Atoi(strings.TrimSpace(a.ErrorCode))
	g.problemCode = code
	switch {
	case basicDisplayRE.MatchString(name):
		// Windows' fallback driver on a real card: the vendor's driver is
		// not installed. The PCI id still says whose card it is.
		g.driverMissing = true
		g.Name = vendorDisplayName(vendor) + " graphics card (model unknown: no driver installed)"
		g.VRAMKnown, g.VRAMBytes = false, 0
		g.VRAMSource = "unknown until the vendor's driver is installed"
		g.Note = "Windows is using its basic display driver for this card. Installing the " + vendorDisplayName(vendor) + " driver lets Ollama use it."
	case code == 28:
		g.driverMissing = true
		g.Note = "Windows reports that no driver is installed for this device."
	case code == 22:
		g.Note = "This device is disabled in Device Manager."
	case code != 0:
		g.Note = fmt.Sprintf("Windows reports a problem with this device (Device Manager code %d).", code)
	}
	return g, false
}

// mergeWindowsGPUs combines the present adapters with nvidia-smi. For an
// NVIDIA card, nvidia-smi is authoritative (live memory total, the driver
// version NVIDIA publishes, the compute capability); the registry entry is
// matched to it by PCI id and contributes only its problem state.
func mergeWindowsGPUs(p *Profile, adapters []winAdapter, smi []GPU, smiFound bool) []GPU {
	var fromRegistry []GPU
	for _, a := range adapters {
		g, filtered := windowsAdapterGPU(a)
		if filtered {
			if n := cleanName(a.Name); n != "" {
				p.FilteredAdapters = append(p.FilteredAdapters, n)
			}
			continue
		}
		fromRegistry = append(fromRegistry, g)
	}

	out := make([]GPU, 0, len(fromRegistry)+len(smi))
	used := make([]bool, len(fromRegistry))
	for _, s := range smi {
		for i, r := range fromRegistry {
			if used[i] || r.Vendor != VendorNVIDIA || r.driverMissing {
				continue
			}
			if s.PCIID != "" && r.PCIID != "" && s.PCIID != r.PCIID {
				continue
			}
			used[i] = true
			if r.Note != "" && s.Note == "" {
				s.Note = r.Note
			}
			s.problemCode = r.problemCode
			break
		}
		out = append(out, s)
	}
	nvidiaFromRegistry := false
	for i, r := range fromRegistry {
		if used[i] {
			continue
		}
		if r.Vendor == VendorNVIDIA && !r.driverMissing {
			nvidiaFromRegistry = true
		}
		out = append(out, r)
	}
	if nvidiaFromRegistry && !smiFound {
		p.problem("nvidia-smi was not found, so the NVIDIA card's details come from the registry and its compute capability is unknown")
	}
	return out
}

// windowsModelsDir: OLLAMA_MODELS from this process, then the user's and the
// machine's saved environment (set after the daemon started, or for a
// service), then Ollama's documented default.
func windowsModelsDir(e env, w winProbe) (dir, source string) {
	if v := strings.TrimSpace(e.getenv("OLLAMA_MODELS")); v != "" {
		return v, "OLLAMA_MODELS"
	}
	if v := strings.TrimSpace(w.OllamaModelsUser); v != "" {
		return v, "OLLAMA_MODELS (your user environment)"
	}
	if v := strings.TrimSpace(w.OllamaModelsMachine); v != "" {
		return v, "OLLAMA_MODELS (the system environment)"
	}
	home := strings.TrimSpace(e.getenv("USERPROFILE"))
	if home == "" {
		if h, err := e.homeDir(); err == nil {
			home = h
		}
	}
	if home == "" {
		return Unknown, "the home folder could not be found"
	}
	return strings.TrimRight(home, `\/`) + `\.ollama\models`, "Ollama's default on Windows"
}
