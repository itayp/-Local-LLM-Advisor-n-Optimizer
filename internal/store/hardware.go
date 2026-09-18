package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"advisor/internal/hardware"
)

// HardwareProfileRow is one stored detection: a row per daemon start. Rows
// are never updated or deleted, so a benchmark that references a profile id
// stays attributable to the hardware it ran on after the user swaps a GPU.
type HardwareProfileRow struct {
	ID            int64
	CreatedAt     string // RFC 3339 UTC
	Fingerprint   string // hardware.Fingerprint of Profile
	DaemonVersion string // the build that detected it; "" for rows older than migration 0002
	Profile       hardware.Profile
}

// HardwareConfiguration groups the stored profiles that share a
// fingerprint: one physical configuration of the machine, however many
// times the daemon started on it.
type HardwareConfiguration struct {
	Fingerprint     string
	FirstSeen       string
	LastSeen        string
	Starts          int
	LatestProfileID int64
	Latest          hardware.Profile // the most recent profile with this fingerprint
}

// AddHardwareProfile stores p as this start's profile and returns the row.
// The plain columns are the queryable subset; profile_json is the whole
// profile. Unknown values are NULL, never 0 (CLAUDE.md, "Unknown is unknown").
func (s *Store) AddHardwareProfile(ctx context.Context, p hardware.Profile, daemonVersion string) (HardwareProfileRow, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return HardwareProfileRow{}, fmt.Errorf("store: encoding hardware profile: %w", err)
	}
	row := HardwareProfileRow{
		CreatedAt:     Now(),
		Fingerprint:   hardware.Fingerprint(p),
		DaemonVersion: daemonVersion,
		Profile:       p,
	}
	nullBool := func(v, known bool) sql.NullBool { return sql.NullBool{Bool: v, Valid: known} }
	nullBytes := func(v uint64, known bool) sql.NullInt64 { return sql.NullInt64{Int64: int64(v), Valid: known} }
	res, err := s.db.ExecContext(ctx, `INSERT INTO hardware_profiles
		(created_at, os, os_version, arch, hostname, cpu_model, cpu_cores, cpu_avx2, cpu_avx512,
		 ram_bytes, unified_memory, gpu_usable_bytes, is_laptop, tier, summary, profile_json, fingerprint, daemon_version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.CreatedAt, p.OS, p.OSVersion, p.Arch, p.Hostname, p.CPU.Model, p.CPU.CoresLogical,
		nullBool(p.CPU.HasAVX2, p.CPU.VectorKnown), nullBool(p.CPU.HasAVX512, p.CPU.VectorKnown),
		nullBytes(p.RAMBytes, p.RAMKnown), p.UnifiedMemory, nullBytes(p.GPUUsableBytes, p.GPUUsableKnown),
		nullBool(p.IsLaptop, p.LaptopKnown), string(p.Tier), p.Summary, string(body), row.Fingerprint, daemonVersion)
	if err != nil {
		return HardwareProfileRow{}, fmt.Errorf("store: inserting hardware profile: %w", err)
	}
	if row.ID, err = res.LastInsertId(); err != nil {
		return HardwareProfileRow{}, fmt.Errorf("store: hardware profile id: %w", err)
	}
	return row, nil
}

const hardwareProfileColumns = `id, created_at, fingerprint, daemon_version, profile_json`

func scanHardwareProfile(sc interface{ Scan(...any) error }) (HardwareProfileRow, error) {
	var r HardwareProfileRow
	var body string
	if err := sc.Scan(&r.ID, &r.CreatedAt, &r.Fingerprint, &r.DaemonVersion, &body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r, ErrNotFound
		}
		return r, err
	}
	if err := json.Unmarshal([]byte(body), &r.Profile); err != nil {
		return r, fmt.Errorf("store: hardware profile %d: %w", r.ID, err)
	}
	return r, nil
}

// HardwareProfile returns one stored profile by id, or ErrNotFound.
func (s *Store) HardwareProfile(ctx context.Context, id int64) (HardwareProfileRow, error) {
	return scanHardwareProfile(s.db.QueryRowContext(ctx,
		`SELECT `+hardwareProfileColumns+` FROM hardware_profiles WHERE id = ?`, id))
}

// LatestHardwareProfile returns the most recent profile, or ErrNotFound.
func (s *Store) LatestHardwareProfile(ctx context.Context) (HardwareProfileRow, error) {
	return scanHardwareProfile(s.db.QueryRowContext(ctx,
		`SELECT `+hardwareProfileColumns+` FROM hardware_profiles ORDER BY id DESC LIMIT 1`))
}

// HardwareProfileBefore returns the profile stored just before id — the
// previous daemon start — or ErrNotFound on the first start.
func (s *Store) HardwareProfileBefore(ctx context.Context, id int64) (HardwareProfileRow, error) {
	return scanHardwareProfile(s.db.QueryRowContext(ctx,
		`SELECT `+hardwareProfileColumns+` FROM hardware_profiles WHERE id < ? ORDER BY id DESC LIMIT 1`, id))
}

// HardwareConfigurations lists the machine's distinct hardware
// configurations, most recently seen first.
func (s *Store) HardwareConfigurations(ctx context.Context) ([]HardwareConfiguration, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT fingerprint, MIN(created_at), MAX(created_at), COUNT(*), MAX(id)
		FROM hardware_profiles GROUP BY fingerprint ORDER BY MAX(id) DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: hardware history: %w", err)
	}
	var out []HardwareConfiguration
	for rows.Next() {
		var c HardwareConfiguration
		if err := rows.Scan(&c.Fingerprint, &c.FirstSeen, &c.LastSeen, &c.Starts, &c.LatestProfileID); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		r, err := s.HardwareProfile(ctx, out[i].LatestProfileID)
		if err != nil {
			return nil, err
		}
		out[i].Latest = r.Profile
	}
	return out, nil
}
