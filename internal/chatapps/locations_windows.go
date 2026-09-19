//go:build windows

package chatapps

import "path/filepath"

// pathCommand is a CLI companion binary that, if on $PATH, is as strong a
// signal as finding the app itself. Only LM Studio is known to ship one
// (`lms`) as of this build.
func pathCommand(id ID) string {
	if id == IDLMStudio {
		return "lms"
	}
	return ""
}

// windowsApps maps each id to its install folder name under
// %LOCALAPPDATA%\Programs and the executable inside it.
var windowsApps = map[ID][2]string{
	IDOllama:      {"Ollama", "ollama.exe"},
	IDLMStudio:    {"LM Studio", "LM Studio.exe"},
	IDOpenWebUI:   {"open-webui-desktop", "open-webui-desktop.exe"},
	IDJan:         {"Jan", "Jan.exe"},
	IDAnythingLLM: {"AnythingLLM", "AnythingLLM.exe"},
}

// installLocations lists where id's Windows install is known to land:
// every one of these apps ships an Electron or Tauri installer whose
// default, per-user target is %LOCALAPPDATA%\Programs\<name>\<exe>
// (Ollama's own installer uses exactly this layout — internal/backend/
// ollama's findBinary checks %LOCALAPPDATA%\Programs\Ollama\ollama.exe —
// and AnythingLLM's own docs, checked 2026-09-19, confirm
// %LOCALAPPDATA%\Programs\AnythingLLM for theirs). The Open WebUI desktop
// app is new enough (github.com/open-webui/desktop) that its folder name
// is a best guess, not a confirmed install path.
func installLocations(e env, id ID) []string {
	lad := e.getenv("LOCALAPPDATA")
	if lad == "" {
		return nil
	}
	pair, known := windowsApps[id]
	if !known {
		return nil
	}
	return []string{filepath.Join(lad, "Programs", pair[0], pair[1])}
}
