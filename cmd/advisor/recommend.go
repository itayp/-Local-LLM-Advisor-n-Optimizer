package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"advisor/internal/figure"
	"advisor/internal/recommend"
	"advisor/internal/server"
)

// `advisor recommend` asks a RUNNING daemon on this machine what it would
// recommend and prints the answer as text — the developer's way to read
// build-plan step 5's gate on a fleet machine ("is the top recommendation
// one Itay would actually give it, with reasons he would say out loud?")
// without opening a browser; scripts/verify.command and CI use it. Like
// `advisor catalog`, it is never the customer's tool: theirs is the
// Recommend screen, which shows the same answer.
//
//	advisor recommend [-port N] [-purposes chat,coding] [-current NAME]

func isRecommendCommand(args []string) bool { return len(args) > 1 && args[1] == "recommend" }

func runRecommend(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("advisor recommend", flag.ContinueOnError)
	fs.SetOutput(stderr)
	port := fs.Int("port", server.DefaultPort, "the port the running daemon listens on, on 127.0.0.1")
	purposes := fs.String("purposes", "chat", "what the model is for, comma-separated: coding, chat, reasoning, long_context, vision, agentic, writing")
	current := fs.String("current", "", "the installed model to compare with (default: the one that serves the purposes best)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	q := url.Values{"purposes": {*purposes}}
	if *current != "" {
		q.Set("current", *current)
	}
	// The daemon only ever listens on the loopback address (product rule 7).
	endpoint := "http://" + server.LoopbackHost + ":" + strconv.Itoa(*port) + "/api/recommend?" + q.Encode()
	client := &http.Client{Timeout: 3 * time.Minute} // the first request waits for hardware detection
	resp, err := client.Get(endpoint)
	if err != nil {
		fmt.Fprintf(stderr, "advisor recommend: no daemon answered on port %d (start `advisor` first): %v\n", *port, err)
		return 1
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		fmt.Fprintf(stderr, "advisor recommend: reading the answer: %v\n", err)
		return 1
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "advisor recommend: the daemon answered %s: %s\n", resp.Status, strings.TrimSpace(string(body)))
		return 1
	}
	var res recommend.Result
	if err := json.Unmarshal(body, &res); err != nil {
		fmt.Fprintf(stderr, "advisor recommend: the answer is not a recommendation result: %v\n", err)
		return 1
	}
	printRecommendations(stdout, res)
	return 0
}

func printRecommendations(w io.Writer, res recommend.Result) {
	var ps []string
	for _, p := range res.Purposes {
		ps = append(ps, string(p))
	}
	fmt.Fprintf(w, "For: %s — built for the %s path (%s)\n", strings.Join(ps, ", "), res.RuntimePath, res.PathSource)
	if res.Warning != "" {
		fmt.Fprintf(w, "! %s\n", res.Warning)
	}
	if res.GPUNotUsed != nil {
		fmt.Fprintf(w, "  why: %s\n", res.GPUNotUsed.Why)
	}
	if res.Current != nil {
		fmt.Fprintf(w, "You have %s. %s\n", res.Current.Name, res.Current.Verdict)
	}
	if res.Empty != "" {
		fmt.Fprintf(w, "Nothing recommended: %s\n", res.Empty)
	}
	for i, r := range res.Recommendations {
		e := r.Estimate
		fmt.Fprintf(w, "\n%d. %s  —  %s (%s), context %d, %s confidence\n", i+1, r.DisplayName, r.PullName, r.File.Quant, r.NumCtx, r.Confidence)
		speed := "no speed estimate"
		if r.Speed != nil {
			speed = rate(*r.Speed)
		}
		cost := "already installed"
		if !r.Installed {
			cost = fmt.Sprintf("download %.1f GB", float64(r.DownloadBytes)/1e9)
		}
		fmt.Fprintf(w, "   %s · needs %s (%s) · %s\n", speed, gib(e.Memory.Total), e.Category, cost)
		fmt.Fprintf(w, "   %s\n", e.Threshold)
		for _, reason := range r.Reasons {
			fmt.Fprintf(w, "   - %s\n", reason.Text)
		}
		fmt.Fprintf(w, "   confidence: %s\n", r.ConfidenceWhy)
		for _, n := range e.Notes {
			fmt.Fprintf(w, "   note: %s\n", n)
		}
	}
}

// rate and gib keep product rule 4 in the terminal too: an estimate is
// marked ≈ and shown as its range; a measurement is a bare number.
func rate(r figure.Rate) string {
	if r.Source == figure.Measured {
		return fmt.Sprintf("%.1f %s (measured)", r.Value, r.Unit)
	}
	return fmt.Sprintf("≈ %g–%g %s (estimated)", r.Low, r.High, r.Unit)
}

func gib(b figure.Bytes) string {
	s := fmt.Sprintf("%.1f GB", float64(b.Value)/(1<<30))
	if b.Source == figure.Measured {
		return s + " (measured)"
	}
	return "≈ " + s
}
