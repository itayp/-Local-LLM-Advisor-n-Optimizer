package recommend

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"advisor/internal/catalog"
	"advisor/internal/estimate"
	"advisor/internal/figure"
	"advisor/internal/hardware"
)

// Everything the customer reads is built here, from the facts the rules
// used — never free text, never a model's output (ARCHITECTURE.md D-8). The
// copy rule applies (CLAUDE.md): none of VRAM, quantization, GGUF, KV cache,
// context window, tokens/sec or offload appears in a sentence; the numbers
// with their units are figures, which the UI renders with their explainer.

// ExplainerGPUNotUsed is the explainer a "graphics card not used" reason
// links to. Step 7's glossary owns the page; Reason.Detail carries what it
// says about this machine.
const ExplainerGPUNotUsed = "gpu_not_used"

// wordsPerToken turns tokens into the words a person thinks in: English
// text runs at about three words to four tokens.
const wordsPerToken = 0.75

var purposeWords = map[catalog.Purpose]string{
	catalog.PurposeCoding:      "coding",
	catalog.PurposeChat:        "everyday chat",
	catalog.PurposeReasoning:   "working through hard problems",
	catalog.PurposeLongContext: "long documents",
	catalog.PurposeVision:      "looking at images",
	catalog.PurposeAgentic:     "driving tools and multi-step tasks",
	catalog.PurposeWriting:     "writing",
}

// warning is the preface the honest answer needs on this machine, if any.
func warning(m estimate.Machine, pl estimate.Placement) string {
	// The same words internal/hardware's Summary uses for these machines
	// (product rule 6: an honest answer, not a failure screen).
	const fewWords = "That is much slower than a graphics card, so small models suit it best — roughly a few words a second."
	if u := pl.Unused; u != nil {
		return unusedSentence(u, true) + " Models run on the processor instead. " + fewWords
	}
	switch {
	case pl.SharedMemory:
		return "The graphics built into this computer's processor share its memory, so models run at the speed of that memory. " + fewWords
	case pl.Path == hardware.PathCPU:
		return "This computer has no graphics card Ollama can use, so models run on its processor. " + fewWords
	}
	return ""
}

// unusedSentence is the plain-words statement that the graphics card is not
// part of the numbers. named puts the card's name in (the banner); without
// it the sentence is the card's first reason, as the build plan words it.
func unusedSentence(u *estimate.UnusedGPU, named bool) string {
	card := "Your graphics card"
	if named && u.Name != "" {
		card = "Your graphics card (" + u.Name + ")"
	}
	switch u.Kind {
	case estimate.UnusedEstablished:
		return card + " is not being used by Ollama; these are the numbers without it."
	case estimate.UnusedUnsupported:
		return card + " cannot be used by Ollama as it is set up now; these are the numbers without it."
	case estimate.UnusedMemoryUnknown:
		return card + "'s memory could not be read, so the advisor cannot plan for it; these are the numbers without it."
	default:
		return "Whether Ollama can use " + strings.ToLower(card[:1]) + card[1:] + " is not known yet; these are the numbers without it."
	}
}

// card turns a scored candidate into what the UI shows.
func (e *Engine) card(c candidate, pl estimate.Placement, m estimate.Machine, purposes []catalog.Purpose, versus string) Recommendation {
	model := c.entry.Model
	model.Files = []catalog.File{} // the card carries the one file it recommends
	r := Recommendation{
		FamilyID:      c.entry.FamilyID,
		DisplayName:   displayName(c.entry),
		Model:         model,
		File:          c.file,
		PullName:      c.entry.Model.Size.OllamaTag,
		NumCtx:        c.ctx,
		Estimate:      c.est,
		Installed:     c.installed,
		Speed:         c.est.Speed.Generation,
		Score:         round3(c.score),
		Factors:       Factors{round3(c.factors.Purpose), round3(c.factors.Fit), round3(c.factors.Speed), round3(c.factors.Size)},
		VersusCurrent: versus,
	}
	if !c.installed {
		r.DownloadBytes = c.file.Bytes
		if c.projector != nil {
			r.DownloadBytes += c.projector.Bytes
		}
	}

	// 1. The graphics card that is not part of these numbers — first, always.
	if u := pl.Unused; u != nil {
		r.Reasons = append(r.Reasons, Reason{Kind: "warning", Text: unusedSentence(u, false), Explainer: ExplainerGPUNotUsed, Detail: u.Why})
	}
	// 2. Does it fit, and in what.
	r.Reasons = append(r.Reasons, e.fitReasons(c, pl, m, purposes)...)
	// 3. Why this family.
	if pr, ok := purposeReason(c.entry, purposes); ok {
		r.Reasons = append(r.Reasons, pr)
	}
	// 4. How fast.
	r.Reasons = append(r.Reasons, speedReason(c.est))
	// 5. What it costs.
	r.Reasons = append(r.Reasons, costReasons(r, m)...)
	// 6. What it changes.
	if versus != "" {
		r.Reasons = append(r.Reasons, Reason{Kind: "change", Text: versus})
	}

	r.Confidence, r.ConfidenceWhy = confidence(c.est, pl)
	return r
}

// displayName is the family's name with the size a person would say:
// "Qwen3.5 9B", "Gemma 4 E4B", "GLM-4.7-Flash".
func displayName(entry Entry) string {
	name := entry.DisplayName
	if name == "" {
		name = entry.FamilyID
	}
	_, size, ok := strings.Cut(entry.Model.Size.OllamaTag, ":")
	if !ok || size == "" || size == "latest" {
		return name
	}
	return name + " " + strings.ToUpper(size)
}

func deviceWords(pl estimate.Placement, m estimate.Machine) string {
	switch pl.BudgetKind {
	case estimate.BudgetGraphics:
		return "your " + hardware.HumanGB(pl.BudgetBytes) + " graphics card"
	case estimate.BudgetUnified:
		return "the " + hardware.HumanGB(pl.BudgetBytes) + " of memory your Mac's graphics can use"
	}
	if m.Profile.RAMKnown {
		return "this computer's " + hardware.HumanGB(m.Profile.RAMBytes) + " of memory"
	}
	return "this computer's memory"
}

func (e *Engine) fitReasons(c candidate, pl estimate.Placement, m estimate.Machine, purposes []catalog.Purpose) []Reason {
	device := deviceWords(pl, m)
	var out []Reason
	switch c.est.Category {
	case estimate.FitsWithHeadroom:
		if c.ctx >= e.Config.LongContextMin {
			out = append(out, Reason{Kind: "fit", Text: "Fits " + device + " with room for long documents."})
		} else {
			out = append(out, Reason{Kind: "fit", Text: "Fits " + device + " with room to spare."})
		}
	case estimate.Fits:
		out = append(out, Reason{Kind: "fit", Text: "Fits " + device + ", without much room to spare."})
	case estimate.NeedsCPUOffload:
		out = append(out, Reason{Kind: "fit", Text: "Too big for " + device + " alone: part of it runs on the processor, which makes it much slower."})
	}
	if c.wanted > 0 && c.ctx < c.wanted {
		words := int(math.Round(float64(c.ctx)*wordsPerToken/1000)) * 1000
		out = append(out, Reason{Kind: "fit", Text: fmt.Sprintf(
			"Here it can keep only about %s words in mind at once, which is short for %s.", commas(words), purposeWords[longestWanting(e.Config, purposes)])})
	}
	return out
}

// longestWanting is the purpose, of those asked for, that wants the most context.
func longestWanting(cfg Config, purposes []catalog.Purpose) catalog.Purpose {
	best := purposes[0]
	for _, p := range purposes {
		if cfg.PurposeContext[p] > cfg.PurposeContext[best] {
			best = p
		}
	}
	return best
}

func purposeReason(entry Entry, purposes []catalog.Purpose) (Reason, bool) {
	rank := func(p catalog.Purpose) int {
		for i, q := range entry.Purposes {
			if q == p {
				return i
			}
		}
		return -1
	}
	var served []string
	best := -1
	for _, p := range purposes {
		if r := rank(p); r >= 0 {
			served = append(served, purposeWords[p])
			if best < 0 || r < best {
				best = r
			}
		}
	}
	if len(served) == 0 {
		return Reason{}, false
	}
	family := entry.DisplayName
	if family == "" {
		family = entry.FamilyID
	}
	if best == 0 {
		return Reason{Kind: "purpose", Text: fmt.Sprintf("%s is a family people use for %s.", family, list(served))}, true
	}
	return Reason{Kind: "purpose", Text: fmt.Sprintf("%s handles %s too, though it is best known for %s.", family, list(served), purposeWords[entry.Purposes[0]])}, true
}

func speedReason(est estimate.Estimate) Reason {
	g := est.Speed.Generation
	if !est.Speed.Known || g == nil {
		why := strings.TrimPrefix(est.Speed.Unknown, "no speed estimate: ")
		if why == "" {
			why = "the advisor has nothing to base one on"
		}
		return Reason{Kind: "speed", Text: "No speed estimate: " + why}
	}
	pace := func(wordsPerSecond float64) string {
		switch {
		case wordsPerSecond >= 15:
			return "much faster than you can read"
		case wordsPerSecond >= 5:
			return "about as fast as you read"
		}
		return "slower than you read: fine for short answers, slow for long ones"
	}
	if g.Source == figure.Measured {
		w := g.Value * wordsPerToken
		return Reason{Kind: "speed", Text: fmt.Sprintf("Measured on this computer at about %s words a second — %s.", num(w), pace(w))}
	}
	lo, hi := g.Low*wordsPerToken, g.High*wordsPerToken
	return Reason{Kind: "speed", Text: fmt.Sprintf("Estimated to answer at roughly %s to %s words a second — %s.", num(lo), num(hi), pace((lo+hi)/2))}
}

func costReasons(r Recommendation, m estimate.Machine) []Reason {
	if r.Installed {
		return []Reason{{Kind: "size", Text: "Already on this computer: nothing to download."}}
	}
	out := []Reason{{Kind: "size", Text: "About " + downloadSize(r.DownloadBytes) + " to download."}}
	if s := m.Profile.Storage; s.FreeKnown && s.FreeBytes < r.DownloadBytes {
		out = append(out, Reason{Kind: "warning", Text: fmt.Sprintf(
			"The drive Ollama keeps its models on has %s free, which is not enough for it.", downloadSize(s.FreeBytes))})
	}
	return out
}

// current is the installed model the recommendations are compared with.
type currentModel struct {
	Current
	modelID   int64
	billions  float64 // effective parameters; 0 when unknown
	bytes     uint64
	purposes  []catalog.Purpose
	cand      *candidate
	catalogue bool
}

// current picks the model to compare with: the one the user named, else the
// installed model that serves these purposes best, else the largest one.
func (e *Engine) current(m estimate.Machine, pl estimate.Placement, installed []InstalledModel, purposes []catalog.Purpose, prefs Preferences) *currentModel {
	if len(installed) == 0 {
		return nil
	}
	assess := func(in InstalledModel) *currentModel {
		cur := &currentModel{Current: Current{Name: in.Name}, bytes: in.SizeBytes}
		if p, ok := catalog.ParseParameterSize(in.ParameterSize); ok {
			cur.billions = float64(p) / 1e9
		}
		for _, entry := range e.Catalogue {
			if in.CatalogModelID == 0 || entry.Model.ID != in.CatalogModelID {
				continue
			}
			file, projector, ok := e.defaultFile(entry)
			for _, f := range entry.Model.Files {
				if f.ID == in.CatalogFileID && in.CatalogFileID != 0 {
					file, ok = f, true
				}
			}
			if !ok {
				break
			}
			c, _ := e.consider(m, pl, entry, file, projector, purposes, Preferences{}, true)
			c.installed = true
			cur.cand, cur.catalogue, cur.modelID = &c, true, entry.Model.ID
			cur.InCatalogue = true
			cur.purposes = entry.Purposes
			cur.billions = effectiveBillions(entry.Model.Size, file.Header)
			est := c.est
			cur.Estimate = &est
		}
		return cur
	}

	var chosen *currentModel
	if name := catalog.NormalizeTag(prefs.CurrentModel); name != "" {
		for _, in := range installed {
			if catalog.NormalizeTag(in.Name) == name {
				chosen = assess(in)
			}
		}
	}
	if chosen == nil {
		for _, in := range installed {
			cur := assess(in)
			switch {
			case chosen == nil:
				chosen = cur
			case cur.cand != nil && (chosen.cand == nil || cur.cand.score > chosen.cand.score):
				chosen = cur
			case cur.cand == nil && chosen.cand == nil && cur.bytes > chosen.bytes:
				chosen = cur
			}
		}
	}
	chosen.Verdict = currentVerdict(chosen, purposes)
	return chosen
}

func currentVerdict(cur *currentModel, purposes []catalog.Purpose) string {
	if !cur.catalogue || cur.Estimate == nil {
		return "The advisor's list does not cover " + cur.Name + ", so it cannot say how well it suits this computer."
	}
	var s string
	switch cur.Estimate.Category {
	case estimate.FitsWithHeadroom, estimate.Fits:
		s = cur.Name + " runs well on this computer."
	case estimate.NeedsCPUOffload:
		s = cur.Name + " is too big for this computer's graphics memory, so part of it runs on the processor and it is slow."
	default:
		s = cur.Name + " needs more memory than this computer can give it."
	}
	serves := false
	for _, p := range purposes {
		for _, q := range cur.purposes {
			if p == q {
				serves = true
			}
		}
	}
	if !serves {
		var words []string
		for _, p := range purposes {
			words = append(words, purposeWords[p])
		}
		s += " It is not a model made for " + list(words) + "."
	}
	return s
}

// versus says what a candidate changes against the current model, and
// whether that is anything at all — a recommendation that changes nothing
// is dropped.
func (e *Engine) versus(c candidate, cur *currentModel) (string, bool) {
	cfg := e.Config
	if cur.modelID != 0 && cur.modelID == c.entry.Model.ID {
		return "", false // it is the model they have
	}
	lead := "Compared with " + cur.Name + ", which you have: "
	var changes []string

	if !cur.catalogue {
		// All that is known of the current model is its size.
		mine := float64(c.file.Bytes)
		switch ratio := mine / math.Max(1, float64(cur.bytes)); {
		case cur.bytes == 0:
			changes = append(changes, "the advisor's list does not cover that model, so it cannot compare them")
		case ratio >= cfg.NoticeableSizeRatio:
			changes = append(changes, times(ratio)+" the size, so very likely more capable — the advisor's list does not cover that model, so size is all it can compare")
		case ratio <= 1/cfg.NoticeableSizeRatio:
			changes = append(changes, "smaller and lighter ("+fraction(ratio)+" the size) — the advisor's list does not cover that model, so size is all it can compare")
		default:
			changes = append(changes, "about the same size — the advisor's list does not cover that model, so it cannot say more than that this one is checked to fit and suited to what you asked for")
		}
		return lead + strings.Join(changes, "; ") + ".", true
	}

	// Purposes the candidate's family is for and the current one's is not.
	has := func(list []catalog.Purpose, p catalog.Purpose) bool {
		for _, q := range list {
			if q == p {
				return true
			}
		}
		return false
	}
	var gained []string
	for _, p := range c.entry.Purposes {
		if !has(cur.purposes, p) && wantedPurpose(c, p) {
			gained = append(gained, purposeWords[p])
		}
	}
	if len(gained) > 0 {
		changes = append(changes, "made for "+list(gained)+", which that one is not")
	}

	mine := effectiveBillions(c.entry.Model.Size, c.file.Header)
	if cur.billions > 0 && mine > 0 {
		switch ratio := mine / cur.billions; {
		case ratio >= cfg.NoticeableSizeRatio:
			changes = append(changes, times(ratio)+" the size, so noticeably more capable")
		case ratio <= 1/cfg.NoticeableSizeRatio:
			changes = append(changes, "smaller ("+fraction(ratio)+" the size), so less capable but lighter")
		}
	}

	if cc := cur.cand; cc != nil {
		a, b := c.est.Speed.Generation, cc.est.Speed.Generation
		if a != nil && b != nil && b.Value > 0 {
			switch ratio := a.Value / b.Value; {
			case ratio >= cfg.NoticeableSpeedRatio:
				changes = append(changes, times(ratio)+" as fast")
			case ratio <= 1/cfg.NoticeableSpeedRatio:
				changes = append(changes, "slower (about "+fraction(ratio)+" the speed)")
			}
		}
		if !cc.est.Category.FitsOnDevice() && c.est.Category.FitsOnDevice() {
			changes = append(changes, "fits this computer properly, which that one does not")
		}
		if cc.ctx > 0 && float64(c.ctx) >= cfg.NoticeableContextRatio*float64(cc.ctx) {
			changes = append(changes, "keeps "+times(float64(c.ctx)/float64(cc.ctx))+" as much text in mind at once")
		}
	}
	if len(changes) == 0 {
		return "", false
	}
	return lead + strings.Join(changes, "; ") + ".", true
}

// wantedPurpose reports whether p contributed to the candidate's purpose
// fit — a purpose nobody asked for is not a change worth naming.
func wantedPurpose(c candidate, p catalog.Purpose) bool {
	for _, q := range c.purposesAsked {
		if q == p {
			return true
		}
	}
	return false
}

// confidence is derived from which inputs were measured, estimated or
// unknown (PRD §21, last risk):
//
//	low     something is unknown (no speed estimate, a model description
//	        the memory arithmetic could not read in full)
//	high    the fit rests on arithmetic that has been measured (step 0's
//	        gate, or a benchmark of this very configuration), the runtime
//	        has been SEEN taking this path, and the speed is measured or is
//	        an estimate on a path whose parts behave alike (cuda, metal)
//	medium  everything else: all inputs known, at least one of them only
//	        expected, modelled or wide
func confidence(est estimate.Estimate, pl estimate.Placement) (Confidence, string) {
	b := est.Basis
	var limits []string
	level := ConfidenceHigh
	lower := func(to Confidence, why string) {
		if to == ConfidenceLow || (to == ConfidenceMedium && level == ConfidenceHigh) {
			level = to
		}
		limits = append(limits, why)
	}

	switch b.MemoryModel {
	case estimate.MemoryIncomplete:
		lower(ConfidenceLow, "part of this model's description could not be read, so its memory use is less certain")
	case estimate.MemoryModelled:
		lower(ConfidenceMedium, "this kind of model has not been measured on a test machine yet, so its memory use is calculated from its description")
	}
	if b.PathSource != estimate.PathEstablished && b.MemoryModel != estimate.MemoryMeasured {
		if pl.Path == hardware.PathCPU {
			lower(ConfidenceMedium, "Ollama has not run a model on this computer yet")
		} else {
			lower(ConfidenceMedium, "Ollama has not run a model on this computer yet, so that it will use the graphics is expected, not seen")
		}
	}
	switch b.SpeedSource {
	case estimate.SpeedUnknown:
		lower(ConfidenceLow, "there is no speed estimate for this computer yet")
	case estimate.SpeedEstimated:
		if pl.Path != hardware.PathCUDA && pl.Path != hardware.PathMetal || pl.SharedMemory {
			lower(ConfidenceMedium, "speed varies a lot between computers like this one, so the range is wide")
		}
	}

	measured := b.SpeedSource == estimate.SpeedMeasured
	switch {
	case len(limits) == 0 && measured:
		return level, "The speed was measured on this computer, and the memory figure is of a kind that has been checked against real measurements."
	case len(limits) == 0:
		return level, "The memory figure is of a kind that has been checked against real measurements, and Ollama has been seen using this computer's graphics. The speed is still an estimate; testing the model here replaces it with a measurement."
	}
	why := strings.ToUpper(limits[0][:1]) + limits[0][1:]
	if len(limits) > 1 {
		why += "; " + strings.Join(limits[1:], "; ")
	}
	return level, why + ". Testing the model on this computer replaces the estimates with measurements."
}

// ---- words and numbers ------------------------------------------------------

func list(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	case 2:
		return words[0] + " and " + words[1]
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// times says a ratio above one the way people do.
func times(ratio float64) string {
	switch {
	case ratio < 1.75:
		return "about one and a half times"
	case ratio < 2.5:
		return "about twice"
	}
	return "about " + strconv.Itoa(int(math.Round(ratio))) + " times"
}

// fraction says a ratio below one the way people do.
func fraction(ratio float64) string {
	switch {
	case ratio > 0.6:
		return "two thirds"
	case ratio > 0.4:
		return "half"
	case ratio > 0.29:
		return "a third"
	case ratio > 0.22:
		return "a quarter"
	}
	return "a fraction of"
}

// downloadSize writes a file size the way download pages do: decimal
// gigabytes, rounded to what a person would say.
func downloadSize(b uint64) string {
	gb := float64(b) / 1e9
	switch {
	case gb < 0.095:
		return strconv.Itoa(int(math.Max(1, math.Round(gb*1000)))) + " MB"
	case gb < 0.95:
		return strconv.Itoa(int(math.Round(gb*10))*100) + " MB"
	case gb < 3:
		return strings.TrimSuffix(strconv.FormatFloat(gb, 'f', 1, 64), ".0") + " GB"
	}
	return strconv.Itoa(int(math.Round(gb))) + " GB"
}

func num(v float64) string {
	if v >= 10 {
		return strconv.Itoa(int(math.Round(v)))
	}
	return strings.TrimSuffix(strconv.FormatFloat(v, 'f', 1, 64), ".0")
}

func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
