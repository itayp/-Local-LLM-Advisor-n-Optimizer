package hardware

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const mib = 1 << 20

// nvidiaSMIFields is the query both Windows and Linux run. compute_cap is
// only understood by drivers from 2021 on (510+); older drivers reject the
// whole query, so queryNvidiaSMI retries without it.
var (
	nvidiaSMIFields       = []string{"index", "name", "memory.total", "driver_version", "compute_cap", "pci.bus_id", "pci.device_id"}
	nvidiaSMIFieldsNoCC   = []string{"index", "name", "memory.total", "driver_version", "pci.bus_id", "pci.device_id"}
	nvidiaSMIFormatFlag   = "--format=csv,noheader,nounits"
	nvidiaSMIComputeError = "compute_cap"
)

func nvidiaSMIArgs(fields []string) []string {
	return []string{"--query-gpu=" + strings.Join(fields, ","), nvidiaSMIFormatFlag}
}

// queryNvidiaSMI runs nvidia-smi at path and parses its answer. ok is false
// when nvidia-smi did not answer; err then says why.
func queryNvidiaSMI(ctx context.Context, e env, path string) (gpus []GPU, ok bool, err error) {
	out, err := e.run(ctx, timeoutNvidiaSMI, path, nvidiaSMIArgs(nvidiaSMIFields)...)
	fields := nvidiaSMIFields
	if err != nil && strings.Contains(out+err.Error(), nvidiaSMIComputeError) {
		fields = nvidiaSMIFieldsNoCC
		out, err = e.run(ctx, timeoutNvidiaSMI, path, nvidiaSMIArgs(fields)...)
	}
	if err != nil {
		return nil, false, err
	}
	gpus, err = parseNvidiaSMI(out, fields)
	if err != nil {
		return nil, false, err
	}
	return gpus, true, nil
}

// parseNvidiaSMI reads `nvidia-smi --query-gpu=<fields> --format=csv,
// noheader,nounits`: one line per GPU, values separated by ", ", memory in
// MiB, "[N/A]" or "[Not Supported]" where the driver has no value.
func parseNvidiaSMI(out string, fields []string) ([]GPU, error) {
	var gpus []GPU
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		vals := strings.Split(line, ",")
		if len(vals) != len(fields) {
			return nil, fmt.Errorf("nvidia-smi: expected %d fields, got %d in %q", len(fields), len(vals), line)
		}
		g := GPU{
			Vendor:          VendorNVIDIA,
			Name:            Unknown,
			DriverVersion:   Unknown,
			VRAMSource:      "nvidia-smi did not report memory.total",
			ExpectedBackend: PathUnknown,
		}
		for i, f := range fields {
			v := strings.TrimSpace(vals[i])
			if v == "" || strings.HasPrefix(v, "[") { // [N/A], [Not Supported]
				continue
			}
			switch f {
			case "name":
				g.Name = cleanName(v)
			case "memory.total":
				if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
					g.VRAMBytes, g.VRAMKnown = n*mib, true
					g.VRAMSource = "nvidia-smi memory.total"
				}
			case "driver_version":
				g.DriverVersion = v
			case "compute_cap":
				if _, err := parseVersion(v); err == nil {
					g.ComputeCapability = v
				}
			case "pci.bus_id":
				g.busID = normalizeBusID(v)
			case "pci.device_id":
				g.PCIID = pciIDFromNvidia(v)
			}
		}
		gpus = append(gpus, g)
	}
	if len(gpus) == 0 {
		return nil, fmt.Errorf("nvidia-smi listed no GPUs")
	}
	return gpus, nil
}

// normalizeBusID turns nvidia-smi's "00000000:01:00.0" and sysfs's
// "0000:01:00.0" into one form: 4-digit domain, lower case.
func normalizeBusID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	dom, rest, ok := strings.Cut(s, ":")
	if !ok || strings.Count(rest, ":") != 1 {
		return s
	}
	if len(dom) > 4 {
		dom = dom[len(dom)-4:]
	}
	for len(dom) < 4 {
		dom = "0" + dom
	}
	return dom + ":" + rest
}

// pciIDFromNvidia turns nvidia-smi's pci.device_id "0x2C0510DE" (device in
// the high half, vendor in the low half) into "10de:2c05".
func pciIDFromNvidia(s string) string {
	s = strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "0x"), "0X"))
	if len(s) != 8 {
		return ""
	}
	if _, err := strconv.ParseUint(s, 16, 32); err != nil {
		return ""
	}
	return s[4:] + ":" + s[:4]
}

// nvidiaDriverFromWindows turns the Windows driver version NVIDIA writes to
// the registry ("32.0.15.6109") into the version NVIDIA publishes
// ("561.09"): the last five digits of the third and fourth fields. Used only
// when nvidia-smi is missing.
func nvidiaDriverFromWindows(s string) string {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 4 {
		return ""
	}
	digits := parts[2] + parts[3]
	for _, r := range digits {
		if r < '0' || r > '9' {
			return ""
		}
	}
	if len(parts[2]) != 2 || len(parts[3]) != 4 { // NVIDIA's scheme: xx.x.1d.dddd
		return ""
	}
	d := digits[len(digits)-5:]
	major := strings.TrimLeft(d[:3], "0")
	if major == "" {
		return ""
	}
	return major + "." + d[3:]
}
