package ollama

import (
	"context"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"advisor/internal/backend"
	"advisor/internal/hardware"
)

// ObserveLoad implements backend.LoadObserver: it notes where Ollama's
// server log ends now, and the function it returns reads what was written
// after that point — the lines of the load that happened in between — and
// parses them (parseLoadReport). Ollama 0.34 runs llama.cpp's llama-server
// with "--log-verbosity 4" and sends its output to the same log, so the
// load's own lines are there: the devices the weights went to, how many
// layers were offloaded, the cache type, whether flash attention was on,
// and the command line with the context size.
//
// Where no log file exists (a Linux install run by systemd), the journal is
// read from the same moment instead; where neither can be read, the report
// says so and every field stays unknown (D-21).
func (b *Backend) ObserveLoad() func(ctx context.Context) backend.LoadReport {
	path, offset := "", int64(0)
	for _, p := range b.logCandidates() {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			path, offset = p, fi.Size()
			break
		}
	}
	since := time.Now()
	return func(ctx context.Context) backend.LoadReport {
		var text string
		var source string
		switch {
		case path != "":
			t, err := readFrom(path, offset)
			if err != nil {
				return backend.LoadReport{Why: "Ollama's log (" + path + ") could not be read: " + err.Error()}
			}
			text, source = t, path
		case runtime.GOOS == "linux":
			t, err := journalSince(ctx, since)
			if err != nil {
				return backend.LoadReport{Why: "no Ollama log file was found, and the system journal could not be read: " + err.Error()}
			}
			text, source = t, "journalctl -u ollama"
		default:
			return backend.LoadReport{Why: "no Ollama log file was found where Ollama keeps one"}
		}
		r := parseLoadReport(text)
		r.Read = true
		if len(r.Evidence) == 0 {
			r.Why = "Ollama's log (" + source + ") has no lines about loading a model since the test started"
		}
		return r
	}
}

// maxLoadLog bounds how much of the log a report reads: a load writes a few
// hundred lines; anything this large is not one load.
const maxLoadLog = 16 << 20

// readFrom reads path from offset to its end. A file shorter than offset
// was replaced (Ollama rotates server.log when it restarts), so the new
// file is read from its start.
func readFrom(path string, offset int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if fi.Size() < offset {
		offset = 0
	}
	if fi.Size()-offset > maxLoadLog {
		offset = fi.Size() - maxLoadLog
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	b, err := io.ReadAll(io.LimitReader(f, maxLoadLog))
	return string(b), err
}

func journalSince(ctx context.Context, since time.Time) (string, error) {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "journalctl", "-u", "ollama", "--since", "@"+strconv.FormatInt(since.Unix(), 10),
		"--no-pager", "-o", "cat").Output()
	if err != nil {
		return "", err
	}
	if len(out) > maxLoadLog {
		out = out[len(out)-maxLoadLog:]
	}
	return string(out), nil
}

// The lines a load writes, as llama.cpp (b10969, the build Ollama v0.34.2
// ships) and Ollama print them:
//
//	load_tensors: offloaded 33/33 layers to GPU                      src/llama-model.cpp
//	load_tensors:        CUDA0 model buffer size =  4403.49 MiB      (one per device)
//	llama_context: flash_attn    = auto                              src/llama-context.cpp
//	resolve_fused_ops: Flash Attention enabled                       (when auto was resolved)
//	llama_kv_cache: size = 1024.00 MiB (  8192 cells,  32 layers,  1/1 seqs), K (f16): 512.00 MiB, V (f16): 512.00 MiB
//	msg="starting llama-server" cmd="…/llama-server --model … -c 8192 -np 1 … --flash-attn auto"   (Ollama, llm/llama_server.go)
//
// Older Ollama builds with their own runner print "flash_attn = 1" and
// "llama_kv_cache_init: … K (f16)"; the patterns accept both.
var (
	offloadedRE   = regexp.MustCompile(`offloaded (\d+)/(\d+) layers to GPU`)
	flashAttnRE   = regexp.MustCompile(`flash_attn\s*=\s*(auto|enabled|disabled|on|off|1|0|true|false)\b`)
	faResolvedRE  = regexp.MustCompile(`Flash Attention (enabled|not supported, set to disabled)`)
	faRequiredRE  = regexp.MustCompile(`enabling flash_attn since it is required`)
	kvTypeRE      = regexp.MustCompile(`\bK \(([a-z0-9_]+)\):.*\bV \(([a-z0-9_]+)\):`)
	startServerRE = regexp.MustCompile(`msg="?starting llama-server"?`)
	ctxArgRE      = regexp.MustCompile(`(?:^|\s)(?:-c|--ctx-size)\s+(\d+)`)
	parallelArgRE = regexp.MustCompile(`(?:^|\s)(?:-np|--parallel)\s+(\d+)`)
)

// parseLoadReport reads one load's lines. When the text holds more than one
// load, the last one's values win: the load the caller caused is the most
// recent.
func parseLoadReport(text string) backend.LoadReport {
	var r backend.LoadReport
	evidence := func(line string) {
		line = strings.TrimSpace(line)
		if len(line) > 300 {
			line = line[:300] + "…"
		}
		r.Evidence = append(r.Evidence, line)
	}
	devices := map[string]bool{}
	var devOrder []string
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		switch {
		case startServerRE.MatchString(line):
			// A new load starts: what an earlier one said no longer applies.
			r = backend.LoadReport{}
			devices, devOrder = map[string]bool{}, nil
			if m := ctxArgRE.FindStringSubmatch(line); m != nil {
				r.ContextSize, _ = strconv.Atoi(m[1])
			}
			if m := parallelArgRE.FindStringSubmatch(line); m != nil {
				r.Parallel, _ = strconv.Atoi(m[1])
			}
			evidence(line)
		case modelBufferRE.MatchString(line):
			m := modelBufferRE.FindStringSubmatch(line)
			name := m[1] + m[2]
			if p, ok := devicePaths[strings.ToLower(m[1])]; ok && p.UsesGPU() {
				r.RuntimePath = p
			}
			if !devices[name] {
				devices[name] = true
				devOrder = append(devOrder, name)
			}
			evidence(line)
		case offloadedRE.MatchString(line):
			m := offloadedRE.FindStringSubmatch(line)
			r.LayersOnGPU, _ = strconv.Atoi(m[1])
			r.Layers, _ = strconv.Atoi(m[2])
			evidence(line)
		case faResolvedRE.MatchString(line):
			m := faResolvedRE.FindStringSubmatch(line)
			r.FlashAttention, r.FlashAttentionKnown = m[1] == "enabled", true
			evidence(line)
		case faRequiredRE.MatchString(line):
			r.FlashAttention, r.FlashAttentionKnown = true, true
			evidence(line)
		case flashAttnRE.MatchString(line):
			// "auto" alone decides nothing: llama.cpp resolves it after
			// reserving the graph and says so on its own line (above).
			switch flashAttnRE.FindStringSubmatch(line)[1] {
			case "enabled", "on", "1", "true":
				r.FlashAttention, r.FlashAttentionKnown = true, true
			case "disabled", "off", "0", "false":
				r.FlashAttention, r.FlashAttentionKnown = false, true
			}
			evidence(line)
		case kvTypeRE.MatchString(line):
			m := kvTypeRE.FindStringSubmatch(line)
			if m[1] == m[2] {
				r.KVCacheType = m[1]
			} else {
				r.KVCacheType = m[1] + "/" + m[2]
			}
			evidence(line)
		}
	}
	if len(devOrder) > 0 {
		r.Devices = devOrder
		if r.RuntimePath == "" && hasCPUBuffer(devOrder) {
			r.RuntimePath = hardware.PathCPU
		}
	}
	return r
}

func hasCPUBuffer(names []string) bool {
	for _, n := range names {
		if strings.HasPrefix(strings.ToUpper(n), "CPU") {
			return true
		}
	}
	return false
}
