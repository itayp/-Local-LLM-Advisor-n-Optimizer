//go:build darwin

package chatapps

import "path/filepath"

// pathCommand is a CLI companion binary that, if on $PATH, is as strong a
// signal as finding the app bundle itself — checked first because it works
// regardless of where the app bundle was dragged to. Only LM Studio is
// known to ship one (`lms`, step 3's ARCHITECTURE.md note on its local
// REST API); the others have none as of this build.
func pathCommand(id ID) string {
	if id == IDLMStudio {
		return "lms"
	}
	return ""
}

// installLocations lists where id's app bundle is known to land on macOS:
// the system /Applications folder every installer offers by default, and
// the per-user ~/Applications folder some installers use instead (Ollama's
// own findBinary, internal/backend/ollama, checks the same two spots for
// its own app bundle).
func installLocations(e env, id ID) []string {
	name, ok := map[ID]string{
		IDOllama:      "Ollama.app",
		IDLMStudio:    "LM Studio.app",
		IDOpenWebUI:   "Open WebUI.app",
		IDJan:         "Jan.app",
		IDAnythingLLM: "AnythingLLM.app",
	}[id]
	if !ok {
		return nil
	}
	locs := []string{filepath.Join("/Applications", name)}
	if home, err := e.userHomeDir(); err == nil && home != "" {
		locs = append(locs, filepath.Join(home, "Applications", name))
	}
	return locs
}
