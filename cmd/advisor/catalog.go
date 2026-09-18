package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"advisor/internal/backend"
	"advisor/internal/catalog"
	"advisor/internal/catalog/hf"
	"advisor/internal/catalog/refresh"
	"advisor/internal/store"
	"advisor/internal/version"
)

const catalogUsage = `usage:
  advisor catalog refresh [-data-dir DIR] [-family ID,...] [-json] [-v]
      Resolve every size in the curated catalogue against Hugging Face: each
      repo's file list, then the GGUF header of each tracked quant, read with
      range requests (no weights are downloaded). Stores the result in the
      daemon's database and lists installed models the catalogue does not
      know. Exits 1 if any size did not resolve.
  advisor catalog check [FILE]
      Validate families.yaml (the embedded one, or FILE) without the network.
`

// runCatalog is the "advisor catalog ..." subcommand: the curator's tools.
// The customer never needs them (product rule 1); the daemon runs the same
// refresh from POST /api/catalog/refresh and, from step 10, nightly.
func runCatalog(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, catalogUsage)
		return 2
	}
	switch args[0] {
	case "check":
		return catalogCheck(args[1:], stdout, stderr)
	case "refresh":
		return catalogRefresh(args[1:], stdout, stderr)
	case "-h", "-help", "--help", "help":
		fmt.Fprint(stdout, catalogUsage)
		return 0
	}
	fmt.Fprintf(stderr, "advisor catalog: unknown command %q\n\n%s", args[0], catalogUsage)
	return 2
}

func catalogCheck(args []string, stdout, stderr io.Writer) int {
	var (
		cat *catalog.Catalogue
		err error
		src = "the embedded families.yaml"
	)
	if len(args) > 0 {
		src = args[0]
		var b []byte
		if b, err = os.ReadFile(src); err == nil {
			cat, err = catalog.Parse(b)
		}
	} else {
		cat, err = catalog.Default()
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", src, err)
		return 1
	}
	sizes := 0
	for _, f := range cat.Families {
		sizes += len(f.Sizes)
	}
	fmt.Fprintf(stdout, "%s is valid: %d families, %d sizes, tracked quants %s\n",
		src, len(cat.Families), sizes, strings.Join(cat.Quants, " "))
	return 0
}

func catalogRefresh(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("advisor catalog refresh", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", "", "the daemon's data folder (default: the OS's application-data folder, or $ADVISOR_DATA_DIR)")
	only := fs.String("family", "", "refresh only these family ids, comma-separated")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	verbose := fs.Bool("v", false, "log each request-level step")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbPath, err := databasePath(*dataDir)
	if err != nil {
		fmt.Fprintln(stderr, "advisor catalog refresh:", err)
		return 1
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		fmt.Fprintln(stderr, "advisor catalog refresh:", err)
		return 1
	}
	defer st.Close()
	cat, err := catalog.Default()
	if err != nil {
		fmt.Fprintln(stderr, "advisor catalog refresh:", err)
		return 1
	}

	// Bring the installed-model inventory up to date first (local only, no
	// network beyond the runtime on this machine), so the list of models the
	// catalogue does not know is today's.
	for _, b := range backend.All() {
		status, err := b.Detect(ctx)
		if err != nil || status.State != backend.StateRunning {
			continue
		}
		if models, err := b.Models(ctx); err == nil {
			if err := st.UpsertInstalledModels(ctx, b.Name(), models); err != nil {
				log.Warn("storing installed models", "backend", b.Name(), "err", err)
			}
		}
	}

	client := hf.New(version.UserAgent())
	client.Log = log
	opts := refresh.Options{Catalogue: cat, Store: st, HF: client, Log: log, Trigger: "cli"}
	if *only != "" {
		opts.Only = strings.Split(*only, ",")
	}
	fmt.Fprintf(stderr, "refreshing the catalogue into %s ...\n", dbPath)
	rep, err := refresh.Run(ctx, opts)
	if err != nil {
		fmt.Fprintln(stderr, "advisor catalog refresh:", err)
		return 1
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
	} else if err := printReport(ctx, stdout, st, cat, rep); err != nil {
		fmt.Fprintln(stderr, "advisor catalog refresh:", err)
		return 1
	}
	if len(rep.Failures) > 0 || rep.Resolved < rep.Sizes {
		return 1
	}
	return 0
}

func databasePath(dataDir string) (string, error) {
	if dataDir != "" {
		return filepath.Join(dataDir, "advisor.db"), nil
	}
	return store.DefaultPath()
}

// printReport is the curator's view: every size with the quants it
// resolved to, then what needs attention.
func printReport(ctx context.Context, w io.Writer, st *store.Store, cat *catalog.Catalogue, rep refresh.Report) error {
	rows, err := st.CatalogModels(ctx, false)
	if err != nil {
		return err
	}
	byKey := map[store.CatalogKey]store.CatalogModelRow{}
	for _, r := range rows {
		byKey[store.CatalogKey{FamilyID: r.Model.FamilyID, Parameters: r.Model.Size.Parameters}] = r
	}
	for _, f := range cat.Families {
		fmt.Fprintf(w, "%s\n", f.DisplayName)
		for _, sz := range f.Sizes {
			r, ok := byKey[store.CatalogKey{FamilyID: f.ID, Parameters: sz.Parameters}]
			switch {
			case !ok:
				fmt.Fprintf(w, "  ??  %-22s not in the database\n", sz.OllamaTag)
			case r.Model.RefreshError != "":
				fmt.Fprintf(w, "  FAIL %-22s %s\n       %s\n", sz.OllamaTag, sz.HFRepo, r.Model.RefreshError)
			case r.Model.RefreshedAt == "":
				fmt.Fprintf(w, "  --   %-22s %s\n       not resolved yet\n", sz.OllamaTag, sz.HFRepo)
			default:
				var quants []string
				vision := ""
				for _, file := range r.Model.Files {
					if file.Role == catalog.RoleProjector {
						vision = fmt.Sprintf(" + vision encoder %s", gb(file.Bytes))
						continue
					}
					q := fmt.Sprintf("%s %s", file.Quant, gb(file.Bytes))
					if file.Parts > 1 {
						q += fmt.Sprintf(" (%d parts)", file.Parts)
					}
					quants = append(quants, q)
				}
				h := firstModelHeader(r.Model.Files)
				fmt.Fprintf(w, "  ok   %-22s %s\n       %s%s\n       %s, %d layers, %d/%d heads (KV), head dim %s, context %d\n",
					sz.OllamaTag, sz.HFRepo, strings.Join(quants, " · "), vision,
					h.Architecture, h.BlockCount, h.HeadCount, h.HeadCountKV, headDim(h), h.ContextLength)
			}
		}
	}
	fmt.Fprintf(w, "\n%d of %d sizes resolved, %d files; %d headers read, %d from cache; %d requests, %s downloaded (no weights)\n",
		rep.Resolved, rep.Sizes, rep.Files, rep.HeaderReads, rep.CacheHits, rep.Requests, mb(rep.BytesRead))
	if len(rep.Warnings) > 0 {
		fmt.Fprintf(w, "\nWorth a look:\n")
		for _, s := range rep.Warnings {
			fmt.Fprintf(w, "  - %s\n", s)
		}
	}
	if len(rep.Unknown) > 0 {
		fmt.Fprintf(w, "\nInstalled models the catalogue does not know:\n")
		for _, u := range rep.Unknown {
			fmt.Fprintf(w, "  - %s (%s): %s\n", u.Name, u.Backend, u.Note)
		}
	}
	if rep.Stopped != "" {
		fmt.Fprintf(w, "\nSTOPPED: %s.\n", rep.Stopped)
	}
	if len(rep.Failures) > 0 {
		fmt.Fprintf(w, "\nNOT RESOLVED: %d size(s) — see FAIL above.\n", len(rep.Failures))
	}
	return nil
}

func firstModelHeader(files []catalog.File) catalog.GGUFHeader {
	for _, f := range files {
		if f.Role == catalog.RoleModel {
			return f.Header
		}
	}
	return catalog.GGUFHeader{}
}

func headDim(h catalog.GGUFHeader) string {
	if h.KeyLength > 0 {
		return fmt.Sprintf("%d (stated)", h.KeyLength)
	}
	if h.HeadCount > 0 {
		return fmt.Sprintf("%d (computed)", h.EmbeddingLength/h.HeadCount)
	}
	return "unknown"
}

func gb(b uint64) string { return fmt.Sprintf("%.1f GB", float64(b)/1e9) }
func mb(b int64) string  { return fmt.Sprintf("%.1f MB", float64(b)/1e6) }

// isCatalogCommand reports whether the command line is "advisor catalog ...".
func isCatalogCommand(args []string) bool {
	return len(args) > 1 && args[1] == "catalog"
}
