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
	"advisor/internal/catalog"
	"advisor/internal/figure"
	"advisor/internal/recommend"
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
//	advisor bench -cancel-during loading|measuring [-model NAME] …
//	advisor bench -history [-model NAME]

const benchUsage = `usage:
  advisor bench [-port N] [-model NAME] [-num-ctx N] [-prompts 500,2000] [-measure-anyway] [-runs N] [-agree PCT]
      Run the benchmark suite on an installed model through the running daemon
      and print the result. Without -model, the gate's model: the smallest
      installed model of the curated list with at least 3 billion parameters
      that the plan does not refuse (else the largest smaller one). -runs 2
      runs it twice and exits 3 when the two generation rates differ by more
      than -agree percent (default 5).
  advisor bench -cancel-during loading|measuring [...]
      Start a run, cancel it as soon as it is loading the model or timing
      its first prompt, and check that the model is no longer loaded —
      asking the daemon, and Ollama itself. Exits 3 if it is.
  advisor bench -history [-model NAME]
      List the stored runs.
`

// gateMinParameters is where the gate's default model starts. Below about
// 3 billion parameters a model answers so fast that the processor's work
// per token, not the memory, sets its pace, and a busy laptop moves that
// between one load and the next: llama3.2:1b on the M1 Pro (2026-09-19)
// ran 109.5, 101.8 and 103.7 tok/s in three runs while each run's own
// timings agreed within 3.4%. The gate is about the harness, so it measures
// a model whose speed the machine, not its background load, decides.
const gateMinParameters = 3e9

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
	cancelDuring := fs.String("cancel-during", "", "start a run, cancel it while it is \"loading\" or \"measuring\", check nothing is left loaded")
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
	var phase bench.Phase
	switch *cancelDuring {
	case "":
	case string(bench.PhaseLoading), string(bench.PhaseMeasuring):
		phase = bench.Phase(*cancelDuring)
	default:
		fmt.Fprintf(stderr, "advisor bench: -cancel-during is %q or %q\n", bench.PhaseLoading, bench.PhaseMeasuring)
		return 2
	}
	if *model == "" {
		m, why, err := c.pickModel(*numCtx)
		if err != nil {
			fmt.Fprintf(stderr, "advisor bench: %v\n", err)
			return 1
		}
		*model = m
		fmt.Fprintf(stdout, "Testing %s (%s; -model picks another).\n", m, why)
	}
	req := bench.Request{Model: *model, NumCtx: *numCtx, MeasureAnyway: *anyway}
	for _, p := range strings.Split(*prompts, ",") {
		if p = strings.TrimSpace(p); p != "" {
			req.Prompts = append(req.Prompts, p)
		}
	}
	if phase != "" {
		return c.cancelCheck(req, phase)
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
		if *runs > 1 && run.GenTPS == nil {
			fmt.Fprintln(stdout, "NOT COMPARABLE: the run timed no answering speed (see its notes); pick a model with -model")
			return 3
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

// pickModel chooses the model the gate measures when none is named: of the
// installed models the curated list knows (all of them when it knows none),
// the smallest with at least gateMinParameters, then the rest largest
// first — the first whose plan at numCtx is not refused.
func (c *benchCLI) pickModel(numCtx int) (string, string, error) {
	var inv server.InstalledModelsResponse
	if err := c.getJSON("/api/models/installed", &inv); err != nil {
		return "", "", err
	}
	if len(inv.Models) == 0 {
		return "", "", errors.New("no model is installed in Ollama; download one first (ollama pull llama3.2:3b)")
	}
	known := inv.Models[:0:0]
	for _, m := range inv.Models {
		if m.CatalogMatch == "file" {
			known = append(known, m)
		}
	}
	if len(known) == 0 {
		known = inv.Models
	}
	for _, m := range gateOrder(known) {
		q := url.Values{"model": {m.Name}}
		if numCtx > 0 {
			q.Set("num_ctx", strconv.Itoa(numCtx))
		}
		var plan bench.Plan
		if err := c.getJSON("/api/bench/plan?"+q.Encode(), &plan); err != nil || plan.Refusal != "" {
			continue
		}
		of := "installed model"
		if len(known) < len(inv.Models) || known[0].CatalogMatch == "file" {
			of = "installed model the curated list knows"
		}
		if params(m) >= gateMinParameters {
			return m.Name, "the smallest " + of + " with at least 3 billion parameters that fits", nil
		}
		return m.Name, "no " + of + " of 3 billion parameters or more fits, so the largest smaller one; small models vary more between runs", nil
	}
	return "", "", errors.New("every installed model's plan is refused on this machine (it would spill onto the processor); pick one with -model and -measure-anyway")
}

// gateOrder sorts candidates for the gate: at least gateMinParameters,
// smallest first; then the smaller ones, largest first.
func gateOrder(models []server.InstalledModelInfo) []server.InstalledModelInfo {
	out := append([]server.InstalledModelInfo(nil), models...)
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := params(out[i]), params(out[j])
		bi, bj := pi >= gateMinParameters, pj >= gateMinParameters
		switch {
		case bi != bj:
			return bi
		case bi:
			return pi < pj || (pi == pj && out[i].SizeBytes < out[j].SizeBytes)
		default:
			return pi > pj || (pi == pj && out[i].SizeBytes > out[j].SizeBytes)
		}
	})
	return out
}

// params is a model's parameter count as the runtime states it ("8.0B"); 0
// when it does not.
func params(m server.InstalledModelInfo) float64 {
	n, ok := catalog.ParseParameterSize(m.ParameterSize)
	if !ok {
		return 0
	}
	return float64(n)
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
// loaded — as the daemon saw it, and as Ollama itself says. The cancel is
// sent when the run's progress reaches the phase asked for (for
// "measuring", a moment into the first timed request, so an answer is
// being written), not after a fixed time: a small model on a fast machine
// finishes a whole run in twenty seconds.
func (c *benchCLI) cancelCheck(req bench.Request, phase bench.Phase) int {
	started, err := c.start(req)
	if err != nil {
		fmt.Fprintf(c.errOut, "advisor bench: %v\n", err)
		return 1
	}
	fmt.Fprintf(c.out, "\nRun %d: %s — cancelling while %s\n", started.ID, started.Config.Model, phase)
	reached := false
	last, err := c.follow(started.ID, func(p bench.Progress) bool {
		reached = p.Phase == phase
		return !reached
	})
	if err != nil {
		fmt.Fprintf(c.errOut, "advisor bench: %v\n", err)
		return 1
	}
	if !reached {
		fmt.Fprintf(c.out, "  the run ended (%s) before it was %s, so there was nothing to cancel\n", last.Status, phase)
		fmt.Fprintln(c.out, "CANCEL NOT TESTED")
		return 1
	}
	if phase == bench.PhaseMeasuring {
		time.Sleep(300 * time.Millisecond)
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
		fmt.Fprintf(w, "  %-7s %7s %12s %13s %8s %12s %8s\n", "prompt", "tokens", "read tok/s", "answer tok/s", "answered", "first token", "spread")
		for _, p := range r.Results {
			read, gen, ttft, spread := "—", "—", "—", "—"
			if p.PromptTPS != nil {
				read = fmt.Sprintf("%.1f", p.PromptTPS.Value)
			}
			if p.GenTPS != nil {
				gen, spread = fmt.Sprintf("%.1f", p.GenTPS.Value), fmt.Sprintf("%.1f%%", p.SpreadPct)
			}
			if p.TTFT != nil {
				ttft = fmt.Sprintf("%.0f ms", p.TTFT.Value)
			}
			fmt.Fprintf(w, "  %-7s %7d %12s %13s %8d %12s %8s\n", p.Prompt, p.PromptTokens, read, gen, p.GenTokens, ttft, spread)
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
	// Backlog (j): what the measured speeds are good for, per saved purpose.
	for _, line := range recommend.VerdictLine(r.Verdicts) {
		fmt.Fprintf(w, "  %s\n", line)
	}
	for _, v := range r.Verdicts {
		if v.Note != "" {
			fmt.Fprintf(w, "    %s\n", v.Note)
		}
	}
	if r.Comparison != nil {
		fmt.Fprintf(w, "  against the previous run of this configuration (run %d, %s): %+.1f%%\n", r.Comparison.RunID, rate(r.Comparison.GenTPS), r.Comparison.DiffPct)
	}
	for _, s := range r.Skipped {
		fmt.Fprintf(w, "  skipped %s: %s\n", s.Prompt, s.Why)
	}
	for _, p := range r.Results {
		if p.GenUnknown != "" {
			fmt.Fprintf(w, "  no answering speed (%s): %s\n", p.Prompt, p.GenUnknown)
		}
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
