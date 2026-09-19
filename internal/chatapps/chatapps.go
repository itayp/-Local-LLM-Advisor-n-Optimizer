// Package chatapps detects chat apps already on this machine — nothing
// else (ARCHITECTURE.md D-4: a runtime is what runs a model and the
// advisor drives it; a chat app is where the person talks to it, and the
// advisor only detects one and hands off — it never installs a chat app,
// never talks to one, and never drives one). It is built the same way
// internal/backend/ollama finds its own binary: well-known install
// locations per OS, checked without starting or downloading anything.
package chatapps

// ID names one chat app this package knows about.
type ID string

const (
	IDOllama      ID = "ollama"
	IDLMStudio    ID = "lmstudio"
	IDOpenWebUI   ID = "openwebui"
	IDJan         ID = "jan"
	IDAnythingLLM ID = "anythingllm"
)

// App is one chat app: whether it was found on this machine, and — either
// way — where to point the person next. Every field is a string or a
// bool; there is nothing here for product rule 4 to apply to.
type App struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
	// Found is true when a well-known install location for this app was
	// seen on this machine. False means "not seen there" — never treated
	// as "not installed" for certain (D-21): a custom install location is
	// always possible.
	Found bool `json:"found"`
	// Path is where it was found, when Found. "" otherwise.
	Path string `json:"path,omitempty"`
	// Note is a plain-language aside worth knowing about this app right
	// now — Open WebUI's desktop app is new enough that build-plan step 7
	// asked for it to carry one; "" for everything else.
	Note string `json:"note,omitempty"`
	// DownloadURL is where a person can get this app, shown next to it
	// when it was not found. The advisor never fetches this itself
	// (D-4: it never installs a chat app).
	DownloadURL string `json:"download_url"`
}

// catalog is every chat app this package knows about, and the order the
// advisor recommends them in (build-plan step 7): Ollama's own app first
// — it came with the runtime the person just installed — then LM Studio
// and the Open WebUI desktop app, then Jan and AnythingLLM.
var catalog = []struct {
	id          ID
	name        string
	downloadURL string
	note        string
}{
	{IDOllama, "Ollama", "https://ollama.com/download", ""},
	{IDLMStudio, "LM Studio", "https://lmstudio.ai", ""},
	{IDOpenWebUI, "Open WebUI", "https://github.com/open-webui/desktop",
		"A newer desktop app for Open WebUI, still under active development — expect rough edges."},
	{IDJan, "Jan", "https://jan.ai/download", ""},
	{IDAnythingLLM, "AnythingLLM", "https://anythingllm.com/download", ""},
}

// Detect looks for each known chat app on this machine, in catalog order,
// using well-known per-OS install locations (locations.go's per-OS
// files). It reads the filesystem and $PATH only: nothing is started,
// downloaded or installed.
func Detect() []App { return detect(realEnv{}) }

func detect(e env) []App {
	out := make([]App, 0, len(catalog))
	for _, c := range catalog {
		path, found := locate(e, c.id)
		out = append(out, App{
			ID: c.id, Name: c.name, Found: found, Path: path,
			Note: c.note, DownloadURL: c.downloadURL,
		})
	}
	return out
}

// locate checks id's known command name on $PATH first (the strongest
// signal, when the app ships a CLI companion — LM Studio's `lms`, for
// instance — because it works regardless of how the app was installed),
// then the well-known install directories pathLocations(id) lists for
// this OS.
func locate(e env, id ID) (path string, found bool) {
	if cmd := pathCommand(id); cmd != "" {
		if p, err := e.lookPath(cmd); err == nil && p != "" {
			return p, true
		}
	}
	for _, c := range installLocations(e, id) {
		// Existence is the signal, not size: a candidate is a directory
		// for a macOS app bundle and a file everywhere else, and a
		// directory's reported size is not a portable thing to compare.
		if _, _, ok := e.statSize(c); ok {
			return c, true
		}
	}
	return "", false
}

// IDs returns every known app's id, in catalog order — used by tests that
// want to exercise every OS file's table without repeating the list.
func IDs() []ID {
	ids := make([]ID, len(catalog))
	for i, c := range catalog {
		ids[i] = c.id
	}
	return ids
}
