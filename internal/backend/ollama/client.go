package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The wire shapes below mirror Ollama's documented JSON exactly
// (docs/api.md); scripts/probe0/main.go used the same shapes against real
// machines across three runtime backends, so this is not a guess at the
// format.

type tagModel struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	Size       uint64 `json:"size"`
	Digest     string `json:"digest"`
	ModifiedAt string `json:"modified_at"`
	Details    struct {
		Family            string   `json:"family"`
		Families          []string `json:"families"`
		ParameterSize     string   `json:"parameter_size"`
		QuantizationLevel string   `json:"quantization_level"`
	} `json:"details"`
}

type tagsResp struct {
	Models []tagModel `json:"models"`
}

type showResp struct {
	Modelfile string `json:"modelfile"`
	Template  string `json:"template"`
	// Parameters is a block of "key value" lines in Ollama's response, not
	// JSON; kept as the raw text (backend.ModelInfo.Parameters), same as
	// Ollama returns it.
	Parameters string `json:"parameters"`
	Details    struct {
		Family            string   `json:"family"`
		Families          []string `json:"families"`
		ParameterSize     string   `json:"parameter_size"`
		QuantizationLevel string   `json:"quantization_level"`
	} `json:"details"`
	ModelInfo     map[string]any `json:"model_info"`
	ProjectorInfo map[string]any `json:"projector_info"`
	Capabilities  []string       `json:"capabilities"`
}

type psModel struct {
	Name          string `json:"name"`
	Model         string `json:"model"`
	Size          uint64 `json:"size"`
	SizeVRAM      uint64 `json:"size_vram"`
	ExpiresAt     string `json:"expires_at"`
	ContextLength int    `json:"context_length"`
}

type psResp struct {
	Models []psModel `json:"models"`
}

type versionResp struct {
	Version string `json:"version"`
}

// httpClient is the subset of *http.Client the ollama package uses, so
// tests can point it at an httptest.Server via the base URL alone — no
// mocking of the client itself is needed.
type httpClient struct {
	base string
	http *http.Client
}

func newHTTPClient(base string, timeout time.Duration) *httpClient {
	return &httpClient{base: base, http: &http.Client{Timeout: timeout}}
}

func (c *httpClient) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ollama: %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(truncate(string(data), 300)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (c *httpClient) version(ctx context.Context) (string, error) {
	var v versionResp
	if err := c.do(ctx, http.MethodGet, "/api/version", nil, &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

func (c *httpClient) tags(ctx context.Context) (tagsResp, error) {
	var t tagsResp
	err := c.do(ctx, http.MethodGet, "/api/tags", nil, &t)
	return t, err
}

func (c *httpClient) show(ctx context.Context, model string) (showResp, error) {
	var s showResp
	err := c.do(ctx, http.MethodPost, "/api/show", map[string]any{"model": model, "verbose": false}, &s)
	return s, err
}

func (c *httpClient) ps(ctx context.Context) (psResp, error) {
	var p psResp
	err := c.do(ctx, http.MethodGet, "/api/ps", nil, &p)
	return p, err
}

// pull streams /api/pull. It uses the client's own http.Client but with no
// deadline beyond ctx: a multi-gigabyte download on a slow line legitimately
// takes longer than any fixed timeout worth setting for the rest of the
// API (scripts/probe0/main.go made the same choice).
func (c *httpClient) pull(ctx context.Context, model string, progress func(status string, completed, total int64)) error {
	body, err := json.Marshal(map[string]any{"model": model, "stream": true})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama: pull %s: %s: %s", model, resp.Status, strings.TrimSpace(truncate(string(data), 300)))
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if err := dec.Decode(&msg); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if msg.Error != "" {
			return errors.New(msg.Error)
		}
		if progress != nil {
			progress(msg.Status, msg.Completed, msg.Total)
		}
	}
}

// generate streams /api/generate. onEvent is called for every line,
// including the final Done == true one.
func (c *httpClient) generate(ctx context.Context, model, prompt, system, keepAlive string, options map[string]any, onEvent func(genLine) error) error {
	body := map[string]any{"model": model, "stream": true}
	if prompt != "" {
		body["prompt"] = prompt
	}
	if system != "" {
		body["system"] = system
	}
	if keepAlive != "" {
		body["keep_alive"] = keepAlive
	}
	if len(options) > 0 {
		body["options"] = options
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/generate", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama: generate: %s: %s", resp.Status, strings.TrimSpace(truncate(string(data), 300)))
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var line genLine
		if err := dec.Decode(&line); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if onEvent != nil {
			if err := onEvent(line); err != nil {
				return err
			}
		}
		if line.Done {
			return nil
		}
	}
}

// genLine is one line of /api/generate's streamed response.
type genLine struct {
	Response           string `json:"response"`
	Done               bool   `json:"done"`
	DoneReason         string `json:"done_reason"`
	TotalDuration      int64  `json:"total_duration"` // nanoseconds
	LoadDuration       int64  `json:"load_duration"`
	PromptEvalCount    int    `json:"prompt_eval_count"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	EvalCount          int    `json:"eval_count"`
	EvalDuration       int64  `json:"eval_duration"`
}

// unload frees a model's memory now: a generate call with keep_alive: 0 and
// no prompt, exactly as Ollama's own docs describe unloading.
func (c *httpClient) unload(ctx context.Context, model string) error {
	body := map[string]any{"model": model, "keep_alive": 0, "stream": false}
	return c.do(ctx, http.MethodPost, "/api/generate", body, nil)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---------------------------------------------------------------------------
// model_info helpers — reading Ollama's architecture-prefixed GGUF metadata.
// Ported from scripts/probe0/main.go, which validated this against real
// models across three machines (step 0, ARCHITECTURE.md D-20): some
// architectures give a per-layer array where a single number is expected,
// in which case the maximum is used.
// ---------------------------------------------------------------------------

func miValue(mi map[string]any, arch, key string) (any, bool) {
	if mi == nil {
		return nil, false
	}
	if arch != "" {
		if v, ok := mi[arch+"."+key]; ok {
			return v, true
		}
	}
	if v, ok := mi[key]; ok {
		return v, true
	}
	suffix := "." + key
	for k, v := range mi {
		if strings.HasSuffix(k, suffix) {
			return v, true
		}
	}
	return nil, false
}

func miNum(mi map[string]any, arch, key string) (float64, bool) {
	v, ok := miValue(mi, arch, key)
	if !ok {
		return 0, false
	}
	return toNum(v)
}

// toNum accepts a number, a numeric string, or a per-layer array (in which
// case it takes the maximum — a few architectures give head counts per
// block).
func toNum(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	case []any:
		max := math.Inf(-1)
		found := false
		for _, e := range t {
			if f, ok := toNum(e); ok {
				found = true
				if f > max {
					max = f
				}
			}
		}
		if !found {
			return 0, false
		}
		return max, true
	}
	return 0, false
}

func archOf(mi map[string]any) string {
	if mi == nil {
		return ""
	}
	if v, ok := mi["general.architecture"].(string); ok {
		return v
	}
	return ""
}
