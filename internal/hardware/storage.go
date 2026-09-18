package hardware

import (
	"context"
	"path"
	"strings"
)

// detectStorage reads the free space on the volume that holds (or will
// hold) the models. The OS paths chose Storage.ModelsDir; when the folder
// does not exist yet — Ollama is not installed — the space is read on its
// nearest existing parent, which is the volume the folder will be created on.
func detectStorage(ctx context.Context, e env, p *Profile) {
	_ = ctx
	dir := p.Storage.ModelsDir
	if dir == "" || dir == Unknown {
		p.problem("the models folder is unknown, so free disk space was not read")
		return
	}
	exists := func(x string) bool {
		if p.OS == "linux" && e.files() != nil {
			return fsExists(e.files(), x)
		}
		return e.exists(x)
	}
	target := dir
	p.Storage.ModelsDirExists = exists(dir)
	if !p.Storage.ModelsDirExists {
		for {
			parent := parentDir(target, p.OS)
			if parent == target {
				break
			}
			target = parent
			if exists(target) {
				break
			}
		}
	}
	free, err := e.diskFree(target)
	if err != nil {
		p.problem("free disk space could not be read for %s: %v", target, err)
		return
	}
	p.Storage.FreeBytes, p.Storage.FreeKnown = free, true
}

// parentDir is filepath.Dir for the target OS's syntax, so the Windows path
// logic is tested on every runner.
func parentDir(dir, goos string) string {
	if goos == "windows" {
		d := strings.TrimRight(dir, `\/`)
		i := strings.LastIndexAny(d, `\/`)
		switch {
		case i < 0:
			return dir
		case i == 2 && len(d) > 1 && d[1] == ':': // "C:\Users" -> "C:\"
			return d[:3]
		case i < 2:
			return dir
		}
		return d[:i]
	}
	return path.Dir(dir)
}
