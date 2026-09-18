package store

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// schemaV0Tables is the contract step 1 promised. A later migration may add
// tables; it may not silently drop one of these.
var schemaV0Tables = []string{
	"backends",
	"benchmark_runs",
	"benchmark_samples",
	"catalog_external",
	"catalog_files",
	"catalog_models",
	"estimates",
	"hardware_profiles",
	"installed_models",
	"schema_migrations",
	"settings",
	"watch_state",
}

// laterTables are the tables later migrations added, so a new table is a
// line a reviewer sees here too.
var laterTables = []string{
	"catalog_refreshes", // 0003, step 4
	"hf_header_cache",   // 0003, step 4
	"hf_listing_cache",  // 0003, step 4
}

func openTemp(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nested", "advisor.db") // the parent must be created
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenAppliesSchemaV0(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.Tables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := append(slices.Clone(schemaV0Tables), laterTables...)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tables after migration:\n got %v\nwant %v", got, want)
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v < 1 {
		t.Fatalf("schema version %d, want >= 1", v)
	}
}

func TestOpenTwiceIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "advisor.db")
	ctx := context.Background()
	s1, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	v1, _ := s1.SchemaVersion(ctx)
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open must not re-run migrations or fail: %v", err)
	}
	defer s2.Close()
	v2, _ := s2.SchemaVersion(ctx)
	if v1 != v2 {
		t.Fatalf("schema version changed between opens: %d -> %d", v1, v2)
	}
}

func TestPragmas(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	var mode string
	if err := s.DB().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := s.DB().QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatal("foreign_keys must be ON")
	}
}

func TestSourceColumnIsConstrained(t *testing.T) {
	// Product rule 4 at the storage layer: an estimates row cannot carry a
	// source that is neither estimated nor measured.
	s := openTemp(t)
	ctx := context.Background()
	db := s.DB()
	now := Now()
	if _, err := db.ExecContext(ctx, `INSERT INTO hardware_profiles
		(created_at, os, os_version, arch, hostname, cpu_model, tier, summary, profile_json, fingerprint)
		VALUES (?, 'linux', 'unknown', 'amd64', 'unknown', 'unknown', 'unknown', '', '{}', 'fp')`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_models
		(created_at, family_id, display_name, parameters, context_length) VALUES (?, 'fam', 'Fam', 1, 1)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_files
		(created_at, catalog_model_id, filename, quant, bytes, fetched_at) VALUES (?, 1, 'f.gguf', 'Q4_K_M', 1, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	insert := func(source string) error {
		_, err := db.ExecContext(ctx, `INSERT INTO estimates
			(created_at, updated_at, hardware_profile_id, catalog_file_id, num_ctx, effective_ctx, runtime_path,
			 weights_bytes, kv_bytes, overhead_bytes, total_bytes, category, source)
			VALUES (?, ?, 1, 1, 4096, 4096, 'cuda', 1, 1, 1, 3, 'fits', ?)`, now, now, source)
		return err
	}
	if err := insert("estimated"); err != nil {
		t.Fatalf("estimated must be accepted: %v", err)
	}
	if err := insert("guessed"); err == nil {
		t.Fatal("a source other than estimated/measured must be rejected by the CHECK constraint")
	}
}

func TestDefaultDataDirHonoursOverride(t *testing.T) {
	t.Setenv("ADVISOR_DATA_DIR", "/tmp/advisor-test-override")
	dir, err := DefaultDataDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/tmp/advisor-test-override" {
		t.Fatalf("DefaultDataDir = %q", dir)
	}
	p, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "advisor.db" {
		t.Fatalf("DefaultPath = %q", p)
	}
}

func TestMigrationsAreWellNamed(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || ms[0].version != 1 || ms[0].name != "0001_schema_v0.sql" {
		t.Fatalf("expected 0001_schema_v0.sql first, got %+v", ms)
	}
	for i := 1; i < len(ms); i++ {
		if ms[i].version != ms[i-1].version+1 {
			t.Fatalf("migrations must be consecutive: %s then %s", ms[i-1].name, ms[i].name)
		}
	}
}
