package hardware

import (
	"fmt"
	"regexp"
	"strings"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// vendorFromPCI maps a PCI vendor id (hex, any case, with or without 0x).
func vendorFromPCI(id string) Vendor {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(id), "0x"), "0X")) {
	case "10de":
		return VendorNVIDIA
	case "1002", "1022":
		return VendorAMD
	case "8086":
		return VendorIntel
	case "106b":
		return VendorApple
	case "5143", "17cb":
		return VendorQualcomm
	}
	return VendorUnknown
}

// pciVendorID is the PCI vendor id of a GPU vendor, for tools that report a
// device id without one (system_profiler on Intel Macs).
func pciVendorID(v Vendor) string {
	switch v {
	case VendorNVIDIA:
		return "10de"
	case VendorAMD:
		return "1002"
	case VendorIntel:
		return "8086"
	}
	return ""
}

var (
	nvidiaNameRE   = regexp.MustCompile(`(?i)\bnvidia\b|\bgeforce\b|\bquadro\b|\btesla\b`)
	amdNameRE      = regexp.MustCompile(`(?i)\bamd\b|\bradeon\b|\bati\b|\bfirepro\b|advanced micro devices`)
	intelNameRE    = regexp.MustCompile(`(?i)\bintel\b|\biris\b|\buhd graphics\b|\bhd graphics\b`)
	appleNameRE    = regexp.MustCompile(`(?i)\bapple\b`)
	qualcommNameRE = regexp.MustCompile(`(?i)\bqualcomm\b|\badreno\b|\bsnapdragon\b`)
)

// vendorFromName is the fallback when no PCI id is available.
func vendorFromName(s string) Vendor {
	switch {
	case nvidiaNameRE.MatchString(s):
		return VendorNVIDIA
	case amdNameRE.MatchString(s):
		return VendorAMD
	case intelNameRE.MatchString(s):
		return VendorIntel
	case appleNameRE.MatchString(s):
		return VendorApple
	case qualcommNameRE.MatchString(s):
		return VendorQualcomm
	}
	return VendorUnknown
}

func vendorDisplayName(v Vendor) string {
	switch v {
	case VendorNVIDIA:
		return "NVIDIA"
	case VendorAMD:
		return "AMD"
	case VendorIntel:
		return "Intel"
	case VendorApple:
		return "Apple"
	case VendorQualcomm:
		return "Qualcomm"
	}
	return "Unknown"
}

// laptopFromChassis reads an SMBIOS chassis type (the same numbers on
// Linux's /sys/class/dmi/id/chassis_type and Windows' Win32_SystemEnclosure).
// known is false for "Other", "Unknown" and the types that say nothing
// about portability.
func laptopFromChassis(t int) (laptop, known bool) {
	switch t {
	case 8, 9, 10, 11, 14, 30, 31, 32: // Portable, Laptop, Notebook, Hand Held, Sub Notebook, Tablet, Convertible, Detachable
		return true, true
	case 3, 4, 5, 6, 7, 13, 15, 16, 17, 23, 24, 28, 35, 36: // Desktop … Tower, All in One, servers, Mini PC, Stick PC
		return false, true
	}
	return false, false
}
