package bench

import "time"

// Config holds every constant the harness uses, each with its reason —
// MEASURED where something measured it, CHOSEN (and what would settle it)
// where nothing has yet. What is sent to the model is not here: that is the
// suite (data/bench/suite.yaml), which is versioned with the runs it makes.
type Config struct {
	// SampleInterval is the resource sampler's period. The build plan's 1 Hz:
	// fast enough to catch the peak of a load that takes seconds, slow
	// enough that nvidia-smi, vm_stat and ioreg — each a process started
	// per reading — cost nothing measurable beside a model.
	SampleInterval time.Duration

	// Settle is the pause between the model leaving memory and the reading
	// the rise is measured from. CHOSEN (2 s): step 0 used 4 s between an
	// unload and a sample and saw no drift after the first second; the
	// sampler's first reading after the pause is the baseline.
	Settle time.Duration

	// UnloadWait is how long the harness waits for the runtime to drop a
	// model after asking it to, polling its list of loaded models every
	// UnloadPoll. CHOSEN: Ollama frees a model within a second or two; a
	// load still in progress when a run is cancelled has to finish first,
	// which for a large model from a slow disk is tens of seconds.
	UnloadWait time.Duration
	UnloadPoll time.Duration

	// KeepAlive is what every benchmark request asks the runtime to keep the
	// model loaded for. The harness unloads it itself at the end; this bounds
	// how long it stays if the daemon dies mid-run.
	KeepAlive string

	// ContextMargin is the room, in tokens, kept free beside a prompt and
	// its answer when deciding whether they fit the context. CHOSEN: the
	// lead line and the tokens a tokenizer adds (a begin-of-text token)
	// are a handful; 64 is the handful with room to spare.
	ContextMargin int

	// MaxCachedShare is the share of a prompt the runtime may reuse from its
	// cache before the prompt rate stops being a measurement of reading the
	// prompt. The suite's numbered lead keeps it to the first token or two
	// (the begin-of-text token): 0.02 of the shortest prompt is ten tokens.
	MaxCachedShare float64

	// SpreadNote is the spread of a prompt's timed runs above which the
	// result says the runs disagreed. The gate's own tolerance (5%,
	// BUILD_PLAN.md step 6): above it, something else was using the machine.
	SpreadNote float64

	// ShortAnswerShare is the share of the answer's budget below which an
	// answer is "short": the model stopped on its own and the rate rests on
	// fewer tokens than planned. CHOSEN: half.
	ShortAnswerShare float64

	// LoadGBs is the range of speeds a model file loads at, for the planned
	// duration only. CHOSEN (0.3–2 GB/s): a file in the operating system's
	// cache loads at memory speed, one read from a hard disk at a fraction of
	// this; the first run's load_duration is the measurement.
	LoadGBsLow, LoadGBsHigh float64

	// SampleBatch is how many samples are written to the store at a time.
	SampleBatch int

	// ProgressBuffer is how many progress events a slow reader of the
	// stream may fall behind by before older ones are dropped (each event
	// carries the whole run, so dropping one loses nothing).
	ProgressBuffer int
}

// DefaultConfig returns the harness's shipped constants.
func DefaultConfig() Config {
	return Config{
		SampleInterval:   time.Second,
		Settle:           2 * time.Second,
		UnloadWait:       60 * time.Second,
		UnloadPoll:       500 * time.Millisecond,
		KeepAlive:        "5m",
		ContextMargin:    64,
		MaxCachedShare:   0.02,
		SpreadNote:       0.05,
		ShortAnswerShare: 0.5,
		LoadGBsLow:       0.3,
		LoadGBsHigh:      2,
		SampleBatch:      10,
		ProgressBuffer:   8,
	}
}
