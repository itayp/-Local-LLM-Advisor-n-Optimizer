package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"advisor/internal/bench"
	"advisor/internal/figure"
	"advisor/internal/server"
)

// `advisor bench` runs a benchmark through a RUNNING daemon on this machine
// and prints it as text — the developer's instrument for build-plan step
// 6's gate ("two consecutive runs of the same configuration agree within
// 5% on generation tok/s, and a cancelled run leaves nothing loaded"),
// which scripts/verify.command runs. Like `advisor recommend`, it is never
// the customer's tool: theirs is the Benchmarks screen, on the same API.
//
//	advisor bench [-port N] [-model NAME] [-num-ctx N] [-prompts 500,2000]
//	              [-measure-anyway] [-runs N] [-agree PCT]
//	advisor bench -cancel-after 10s [-model NAME] …
//	advisor bench -history [-model NAME]

const benchUsage = `usage:
  advisor bench [-port N] [-model NAME] [-num-ctx N] [-prompts 500,2000] [-measure-anyway] [-runs N] [-agree PCT]
      Run the benchmark suite on an installed model through the running daemon
      and print the result. Without -model, the smallest installed model the
      curated list knows. -runs 2 runs it twice and exits 3 when the two
      generation rates differ by more than -agree percent (default 5).
  advisor bench -cancel-after DURATION [...]
      Start a run, cancel it after DURATION, and check that the model is no
      longer loaded — asking the daemon, and Ollama itself. Exits 3 if it is.
  advisor bench -history [-model NAME]
      List the stored runs.
`

func isBenchCommand(args []string) bool { return len(args) > 1 && args[1] == "bench" }

type benchCLI struct {
	base   string // the daemon, http://127.0.0.1:port
	client *http.Client
	out    io.Writer
	errOut io.Writer
}

func runBench(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("advisor bench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, benchUsage) }
	port := fs.Int("port", server.DefaultPort, "the port the running daemon listens on, on 127.0.0.1")
	model := fs.String("model", "", "the installed model to test (default: the smallest one the curated list knows)")
	numCtx := fs.Int("num-ctx", 0, "the context to test (default: what Ollama uses on this machine)")
	prompts := fs.String("prompts", "", "comma-separated prompt ids (default: the whole suite)")
	anyway := fs.Bool("measure-anyway", false, "run a configuration the estimate says would spill onto the processor")
	runs := fs.Int("runs", 1, "how many runs, one after the other")
	agree := fs.Float64("agree", 5, "with -runs 2 or more: the largest difference, in percent, between consecutive generation rates")
	cancelAfter := fs.Duration("cancel-after", 0, "start a run, cancel it after this long, check nothing is left loaded")
	history := fs.Bool("history", false, "list the stored runs")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	c := &benchCLI{
		// The daemon only ever listens on the loopback address (product rule 7).
		base:   "http://" + server.LoopbackHost + ":" + strconv.Itoa(*port),
		client: &http.Client{Timeout: 3 * time.Minute},
		out:    stdout, errOut: stderr,
	}
	if *history {
		return c.history(*model)
	}
	if *model == "" {
		m, err := c.pickModel()
		if err != nil {
			fmt.Fprintf(stderr, "advisor bench: %v\n", err)
			return 1
		}
		*model = m
		fmt.Fprintf(stdout, "Testing %s (the smallest installed model the curated list knows; -model picks another).\n", m)
	}
	req := bench.Request{Model: *model, NumCtx: *numCtx, MeasureAnyway: *anyway}
	for _, p := range strings.Split(*prompts, ",") {
		if p = strings.TrimSpace(p); p != "" {
			req.Prompts = append(req.Prompts, p)
		}
	}
	if *cancelAfter > 0 {
		return c.cancelCheck(req, *cancelAfter)
	}

	var done []bench.Run
	for i := 0; i < max(*runs, 1); i++ {
		run, err := c.run(req)
		if err != nil {
			fmt.Fprintf(stderr, "advisor bench: %v\n", err)
			return 1
		}
		printRun(stdout, run)
		if run.Status != bench.StatusDone {
			return 1
		}
		done = append(done, run)
	}
	if len(done) < 2 {
		return 0
	}
	worst := 0.0
	fmt.Fprintln(stdout)
	for i := 1; i < len(done); i++ {
		a, b := done[i-1].GenTPS.Value, done[i].GenTPS.Value
		d := 100 * (b - a) / a
		worst = math.Max(worst, math.Abs(d))
		fmt.Fprintf(stdout, "Run %d against run %d: answering %.1f → %.1f tok/s (%+.1f%%)\n", done[i].ID, done[i-1].ID, a, b, d)
	}
	if worst > *agree {
		fmt.Fprintf(stdout, "NOT REPEATABLE: consecutive runs differ by %.1f%%, more than %.0f%%\n", worst, *agree)
		return 3
	}
	fmt.Fprintf(stdout, "REPEATABLE: consecutive runs agree within %.1f%% (limit %.0f%%)\n", worst, *agree)
	return 0
}

// pickModel is the smallest installed model the catalogue knows, else the
// smallest installed model.
func (c *benchCLI) pickModel() (string, error) {
	var inv server.InstalledModelsResponse
	if err := c.getJSON("/api/models/installed", &inv); err != nil {
		return "", err
	}
	if len(inv.Models) == 0 {
		return "", errors.New("no model is installed in Ollama; download one first (ollama pull llama3.2:3b)")
	}
	sort.SliceStable(inv.Models, func(i, j int) bool {
		ki, kj := inv.Models[i].CatalogMatch == "file", inv.Models[j].CatalogMatch == "file"
		if ki != kj {
			return ki
		}
		return inv.Models[i].SizeBytes < inv.Models[j].SizeBytes
	})
	return inv.Models[0].Name, nil
}

// run starts a run and follows its stream to the end, printing each new
// message.
func (c *benchCLI) run(req bench.Request) (bench.Run, error) {
	started, err := c.start(req)
	if err != nil {
		return bench.Run{}, err
	}
	fmt.Fprintf(c.out, "\nRun %d: %s", started.ID, started.Config.Model)
	if started.ExpectedDuration != nil {
		fmt.Fprintf(c.out, " — estimated %s", seconds(*started.ExpectedDuration))
	}
	fmt.Fprintln(c.out)
	last, err := c.follow(started.ID, nil)
	if err != nil {
		return bench.Run{}, err
	}
	// The stored run, with everything the stream's last event carries and
	// the comparison with the run before it.
	var run bench.Run
	if err := c.getJSON("/api/bench/"+strconv.FormatInt(last.RunID, 10), &run); err != nil {
		return last.Run, nil
	}
	run.Samples = nil
	return run, nil
}

func (c *benchCLI) start(req bench.Request) (bench.Run, error) {
	body, _ := json.Marshal(req)
	resp, err := c.client.Post(c.base+"/api/bench", "application/json", bytes.NewReader(body))
	if err != nil {
		return bench.Run{}, fmt.Errorf("no daemon answered at %s (start `advisor` first): %v", c.base, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusAccepted {
		return bench.Run{}, apiError(resp.Status, b)
	}
	var run bench.Run
	if err := json.Unmarshal(b, &run); err != nil {
		return bench.Run{}, fmt.Errorf("the daemon's answer is not a run: %v", err)
	}
	return run, nil
}

// follow reads the run's progress stream until it ends. onEvent, when set,
// sees every event and may stop following by returning false.
func (c *benchCLI) follow(id int64, onEvent func(bench.Progress) bool) (bench.Progress, error) {
	req, _ := http.NewRequest(http.MethodGet, c.base+"/api/bench/"+strconv.FormatInt(id, 10), nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := (&http.Client{}).Do(req) // no timeout: a run takes as long as it takes
	if err != nil {
		return bench.Progress{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return bench.Progress{}, apiError(resp.Status, b)
	}
	var last bench.Progress
	msg := ""
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var p bench.Progress
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return last, fmt.Errorf("a progress event could not be read: %v", err)
		}
		last = p
		if p.Message != msg {
			msg = p.Message
			fmt.Fprintf(c.out, "  %5.0fs  %s\n", p.ElapsedSeconds, p.Message)
		}
		if onEvent != nil && !onEvent(p) {
			return last, nil
		}
		if p.Status.Finished() {
			return last, nil
		}
	}
	if err := sc.Err(); err != nil {
		return last, err
	}
	return last, nil
}

// cancelCheck is the gate's second half: a cancelled run leaves nothing
// loaded — as the daemon saw it, and as Ollama itself says.
func (c *benchCLI) cancelCheck(req bench.Request, after time.Duration) int {
	started, err := c.start(req)
	if err != nil {
		fmt.Fprintf(c.errOut, "advisor bench: %v\n", err)
		return 1
	}
	fmt.Fprintf(c.out, "\nRun %d: %s — cancelling after %s\n", started.ID, started.Config.Model, after)
	deadline := time.Now().Add(after)
	if _, err := c.follow(started.ID, func(bench.Progress) bool { return time.Now().Before(deadline) }); err != nil {
		fmt.Fprintf(c.errOut, "advisor bench: %v\n", err)
		return 1
	}
	if d := time.Until(deadline); d > 0 {
		time.Sleep(d)
	}
	resp, err := c.client.Post(c.base+"/api/bench/"+strconv.FormatInt(started.ID, 10)+"/cancel", "application/json", nil)
	if err != nil {
		fmt.Fprintf(c.errOut, "advisor bench: cancel: %v\n", err)
		return 1
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(c.errOut, "advisor bench: cancel: %v\n", apiError(resp.Status, b))
		return 1
	}
	var run bench.Run
	_ = json.Unmarshal(b, &run)
	fmt.Fprintf(c.out, "  status %s", run.Status)
	daemonSays := run.Unloaded != nil && *run.Unloaded
	fmt.Fprintf(c.out, "; the daemon confirmed the model unloaded: %s\n", yesNo(daemonSays))

	loaded, err := ollamaLoaded()
	switch {
	case err != nil:
		fmt.Fprintf(c.out, "  Ollama's own list of loaded models could not be read: %v\n", err)
	case len(loaded) == 0:
		fmt.Fprintln(c.out, "  Ollama's own list of loaded models (/api/ps): empty")
	default:
		fmt.Fprintf(c.out, "  Ollama's own list of loaded models (/api/ps): %s\n", strings.Join(loaded, ", "))
	}
	stillThere := false
	for _, name := range loaded {
		stillThere = stillThere || strings.EqualFold(name, run.Config.Model) || strings.EqualFold(name, run.Config.Model+":latest")
	}
	if run.Status != bench.StatusCancelled || !daemonSays || stillThere {
		fmt.Fprintln(c.out, "CANCEL LEFT THE MODEL LOADED (or did not cancel)")
		return 3
	}
	fmt.Fprintln(c.out, "CANCEL OK: nothing of the run is left loaded")
	return 0
}

// ollamaLoaded asks Ollama directly — not through the daemon — what it has
// loaded: the independent half of the cancel check.
func ollamaLoaded() ([]string, error) {
	host := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	if host == "" {
		host = "http://127.0.0.1:11434"
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(strings.TrimRight(host, "/") + "/api/ps")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var ps struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ps); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range ps.Models {
		if m.Model != "" {
			out = append(out, m.Model)
		} else {
			out = append(out, m.Name)
		}
	}
	return out, nil
}

func (c *benchCLI) history(model string) int {
	q := url.Values{}
	if model != "" {
		q.Set("model", model)
	}
	var h bench.History
	if err := c.getJSON("/api/bench/history?"+q.Encode(), &h); err != nil {
		fmt.Fprintf(c.errOut, "advisor bench: %v\n", err)
		return 1
	}
	if len(h.Runs) == 0 {
		fmt.Fprintln(c.out, "No tests have been run yet.")
		return 0
	}
	for _, r := range h.Runs {
		gen := "—"
		if r.GenTPS != nil {
			gen = rate(*r.GenTPS)
		}
		cmp := ""
		if r.Comparison != nil {
			cmp = fmt.Sprintf("  (%+.1f%% against run %d)", r.Comparison.DiffPct, r.Comparison.RunID)
		}
		fmt.Fprintf(c.out, "%4d  %s  %-9s  %-24s ctx %-6d %-6s %-5s  %s%s\n", r.ID, r.StartedAt.Local().Format("2006-01-02 15:04"),
			r.Status, r.Config.Model, r.Config.NumCtx, r.Config.RuntimePath, r.Config.KVCacheType, gen, cmp)
	}
	return 0
}

func (c *benchCLI) getJSON(path string, into any) error {
	resp, err := c.client.Get(c.base + path)
	if err != nil {
		return fmt.Errorf("no daemon answered at %s (start `advisor` first): %v", c.base, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode != http.StatusOK {
		return apiError(resp.Status, b)
	}
	return json.Unmarshal(b, into)
}

func apiError(status string, body []byte) error {
	var e server.APIError
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return fmt.Errorf("%s (%s)", e.Error.Message, e.Error.Code)
	}
	return fmt.Errorf("the daemon answered %s: %s", status, strings.TrimSpace(string(body)))
}

// printRun writes a finished run the way the gate reads it. Product rule 4
// in the terminal too: measurements are bare numbers, estimates carry ≈.
func printRun(w io.Writer, r bench.Run) {
	c := r.Config
	took := ""
	if r.FinishedAt != nil {
		took = fmt.Sprintf(" in %s", r.FinishedAt.Sub(r.StartedAt).Round(time.Second))
	}
	fmt.Fprintf(w, "Run %d: %s (%s) at a context of %d — %s%s\n", r.ID, c.Model, c.Quantization, c.NumCtx, r.Status, took)
	fa := "flash attention unknown"
	if c.FlashAttentionKnown {
		fa = "flash attention " + map[bool]string{true: "on", false: "off"}[c.FlashAttention]
	}
	fmt.Fprintf(w, "  %s %s · path %s · %s cache · %s · runs at %d · suite %s · daemon %s\n",
		c.Backend, c.BackendVersion, c.RuntimePath, c.KVCacheType, fa, c.EffectiveCtx, c.SuiteVersion, c.DaemonVersion)
	if r.Error != "" {
		fmt.Fprintf(w, "  error: %s\n", r.Error)
	}
	if len(r.Results) > 0 {
		fmt.Fprintf(w, "  %-7s %7s %12s %13s %12s %8s\n", "prompt", "tokens", "read tok/s", "answer tok/s", "first token", "spread")
		for _, p := range r.Results {
			read, ttft := "—", "—"
			if p.PromptTPS != nil {
				read = fmt.Sprintf("%.1f", p.PromptTPS.Value)
			}
			if p.TTFT != nil {
				ttft = fmt.Sprintf("%.0f ms", p.TTFT.Value)
			}
			fmt.Fprintf(w, "  %-7s %7d %12s %13.1f %12s %7.1f%%\n", p.Prompt, p.PromptTokens, read, p.GenTPS.Value, ttft, p.SpreadPct)
		}
	}
	var res []string
	if r.Load != nil {
		res = append(res, fmt.Sprintf("load %.0f ms", r.Load.Value))
	}
	res = append(res, "resident "+string(r.Resident))
	if r.PeakVRAM != nil {
		res = append(res, fmt.Sprintf("graphics memory taken %s", gib(*r.PeakVRAM)))
	}
	if r.RuntimeSizeBytes > 0 {
		res = append(res, fmt.Sprintf("Ollama's own size %.1f GB (%.1f GB on the graphics)", float64(r.RuntimeSizeBytes)/(1<<30), float64(r.RuntimeSizeVRAMBytes)/(1<<30)))
	}
	if r.PeakRAM != nil {
		res = append(res, "system memory in use at peak "+gib(*r.PeakRAM))
	}
	if r.GPUUtil != nil {
		res = append(res, fmt.Sprintf("GPU %.0f%%", r.GPUUtil.Value))
	}
	if r.PeakTemp != nil {
		res = append(res, fmt.Sprintf("%.0f °C", r.PeakTemp.Value))
	}
	if r.Power != nil {
		res = append(res, fmt.Sprintf("%.0f W", r.Power.Value))
	}
	fmt.Fprintf(w, "  %s\n", strings.Join(res, " · "))
	if r.MemorySource != "" {
		fmt.Fprintf(w, "  memory from: %s\n", r.MemorySource)
	}
	if r.Estimate != nil && r.GenTPS != nil {
		est := "no speed estimate"
		if g := r.Estimate.Speed.Generation; g != nil {
			est = rate(*g)
		}
		fmt.Fprintf(w, "  estimated before: %s → measured %s; estimate replaced: %s\n", est, rate(*r.GenTPS), yesNo(r.Replaced))
	}
	if r.Comparison != nil {
		fmt.Fprintf(w, "  against the previous run of this configuration (run %d, %s): %+.1f%%\n", r.Comparison.RunID, rate(r.Comparison.GenTPS), r.Comparison.DiffPct)
	}
	for _, s := range r.Skipped {
		fmt.Fprintf(w, "  skipped %s: %s\n", s.Prompt, s.Why)
	}
	for _, p := range r.Results {
		for _, n := range p.Notes {
			fmt.Fprintf(w, "  note (%s): %s\n", p.Prompt, n)
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintf(w, "  note: %s\n", n)
	}
	if r.SamplerNote != "" {
		fmt.Fprintf(w, "  not sampled: %s\n", r.SamplerNote)
	}
	if r.Unloaded != nil {
		fmt.Fprintf(w, "  unloaded afterwards: %s\n", yesNo(*r.Unloaded))
	}
}

func seconds(r figure.Rate) string {
	if r.Source == figure.Measured {
		return fmt.Sprintf("%.0f s", r.Value)
	}
	return fmt.Sprintf("≈ %.0f–%.0f s", r.Low, r.High)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
