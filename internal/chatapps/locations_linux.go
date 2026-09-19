//go:build linux

package chatapps

// pathCommand is a CLI companion binary, or a launcher a .deb/package
// install puts on $PATH — the most reliable signal on Linux, where there
// is no one fixed install location (these apps ship as AppImages and
// .deb packages, not a single installer everyone runs). Checked before
// any directory below.
func pathCommand(id ID) string {
	switch id {
	case IDLMStudio:
		return "lms"
	case IDJan:
		return "jan"
	case IDAnythingLLM:
		return "anythingllm"
	case IDOpenWebUI:
		return "open-webui-desktop"
	default:
		return ""
	}
}

// linuxOpt is a couple of well-known /opt install spots a .deb package
// sometimes uses, best-effort only: an AppImage a person downloaded by
// hand has no fixed location at all, so pathCommand above is what
// actually carries Linux detection. Ollama ships no Linux desktop app
// (CLI and a systemd-style service only, as of this build) so it has no
// entry here — the download link still points at ollama.com.
func linuxOpt(id ID) []string {
	switch id {
	case IDLMStudio:
		return []string{"/opt/LM Studio/lm-studio"}
	case IDOpenWebUI:
		return []string{"/opt/open-webui-desktop/open-webui-desktop"}
	case IDJan:
		return []string{"/opt/Jan/jan"}
	case IDAnythingLLM:
		return []string{"/opt/AnythingLLM/anythingllm"}
	default:
		return nil
	}
}

func installLocations(e env, id ID) []string {
	return linuxOpt(id)
}
