package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// FORCE RLS coverage (docs/lessons-from-b2b.md item 1, RLS bullet: "Her tenant
// tablosunda FORCE ROW LEVEL SECURITY migration'da var mı?").
//
// rls_test.go already proves the MECHANISM: an app_runtime connection with no
// SET LOCAL app.tenant_id sees zero rows, and the table owner cannot bypass
// its own policy. tenant/repo/rls_invariants_test.go proves it per-table for
// one module. Neither proves COVERAGE: a new migration that creates a table
// with tenant_id and forgets `FORCE ROW LEVEL SECURITY` passes both, and the
// table is then silently readable across tenants by its owner role.
//
// This test closes that gap statically. It reads the migration SQL — the
// artefact that is actually reviewed in a PR — rather than a live database,
// so it runs in `task backend:test:unit` with no container and fails in the
// same commit that introduces the table.
//
// Rule: a table whose CREATE TABLE body declares a `tenant_id` column must,
// somewhere in the same module's migrations, carry BOTH
// `ALTER TABLE <t> ENABLE ROW LEVEL SECURITY` and
// `ALTER TABLE <t> FORCE ROW LEVEL SECURITY` (ADR-SEC-001/002 — ENABLE alone
// exempts the owning role, which in this deployment owns the tables).
// ---------------------------------------------------------------------------

var (
	createTableRe  = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z0-9_]+)\s*\((.*?)\n\s*\)[^;]*;`)
	enableRLSRe    = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+(?:ONLY\s+)?([a-z0-9_]+)\s+ENABLE\s+ROW\s+LEVEL\s+SECURITY`)
	forceRLSRe     = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+(?:ONLY\s+)?([a-z0-9_]+)\s+FORCE\s+ROW\s+LEVEL\s+SECURITY`)
	addTenantColRe = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+(?:ONLY\s+)?([a-z0-9_]+)\s+ADD\s+COLUMN\s+(?:IF\s+NOT\s+EXISTS\s+)?tenant_id`)
	renameTableRe  = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+(?:ONLY\s+)?([a-z0-9_]+)\s+RENAME\s+TO\s+([a-z0-9_]+)`)
	dropTableRe    = regexp.MustCompile(`(?i)DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?([a-z0-9_,\s]+?)\s*(?:CASCADE|RESTRICT)?\s*;`)
)

// rlsExempt lists tenant_id-bearing tables that deliberately do NOT carry
// FORCE RLS, each with the reason. An entry here is a reviewed exception, not
// a silent one — and the test below fails if an entry becomes stale.
var rlsExempt = map[string]string{}

// TestEveryTenantTableForcesRLS is the coverage guard described above.
func TestEveryTenantTableForcesRLS(t *testing.T) {
	root := filepath.Join("..", "..", "..", "migrations")

	type tableInfo struct {
		module   string
		file     string
		hasEnab  bool
		hasForce bool
	}
	tables := map[string]*tableInfo{}
	// dropped is order-insensitive: a table dropped in one migration and
	// recreated in a later one would stay exempt. No such pair exists today;
	// if one is ever added, this needs to become a per-migration sequence walk.
	dropped := map[string]bool{}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".up.sql") {
			return nil
		}
		raw, readErr := os.ReadFile(path) //nolint:gosec // test-only read of repo migrations
		if readErr != nil {
			return readErr
		}
		sql := stripSQLComments(string(raw))
		module := filepath.Base(filepath.Dir(path))

		for _, m := range createTableRe.FindAllStringSubmatch(sql, -1) {
			name, body := m[1], m[2]
			if !regexp.MustCompile(`(?im)^\s*tenant_id\b`).MatchString(body) {
				continue
			}
			if _, ok := tables[name]; !ok {
				tables[name] = &tableInfo{module: module, file: filepath.ToSlash(path)}
			}
		}
		// A renamed table is the same table: carry its identity (and the RLS
		// facts recorded so far) forward, or stock_levels/stock_movements —
		// which exist only as renames of inventory_levels/inventory_transactions
		// (migrations/inventory/000003) — would never be checked at all, while
		// their dead former names kept passing.
		for _, m := range renameTableRe.FindAllStringSubmatch(sql, -1) {
			from, to := m[1], m[2]
			if info, ok := tables[from]; ok {
				if existing, clash := tables[to]; clash {
					info.hasEnab = info.hasEnab || existing.hasEnab
					info.hasForce = info.hasForce || existing.hasForce
				}
				tables[to] = info
				delete(tables, from)
			}
		}
		// A table that gained tenant_id later is just as much a tenant table.
		for _, m := range addTenantColRe.FindAllStringSubmatch(sql, -1) {
			if _, ok := tables[m[1]]; !ok {
				tables[m[1]] = &tableInfo{module: module, file: filepath.ToSlash(path)}
			}
		}
		for _, m := range enableRLSRe.FindAllStringSubmatch(sql, -1) {
			if info, ok := tables[m[1]]; ok {
				info.hasEnab = true
			}
		}
		for _, m := range forceRLSRe.FindAllStringSubmatch(sql, -1) {
			if info, ok := tables[m[1]]; ok {
				info.hasForce = true
			}
		}
		for _, m := range dropTableRe.FindAllStringSubmatch(sql, -1) {
			for _, name := range strings.Split(m[1], ",") {
				dropped[strings.TrimSpace(name)] = true
			}
		}
		return nil
	})
	require.NoError(t, err)

	// A regex-based walk that silently stops matching would turn this whole
	// test into a vacuum pass, so the discovered count is pinned. minTenantTables
	// is the number found when this guard was written (2026-09-20); it may only
	// ever be raised. If a migration legitimately drops tables below it, lower it
	// in the same commit — deliberately, not by watching the test keep passing.
	const minTenantTables = 55
	t.Logf("tenant tables discovered: %d", len(tables))
	require.GreaterOrEqualf(t, len(tables), minTenantTables,
		"only %d tenant tables discovered (expected at least %d) — createTableRe has stopped "+
			"matching some CREATE TABLE form, which makes every assertion below meaningless",
		len(tables), minTenantTables)

	var missing []string
	for name, info := range tables {
		if dropped[name] {
			continue
		}
		if _, exempt := rlsExempt[name]; exempt {
			continue
		}
		switch {
		case !info.hasEnab:
			missing = append(missing, name+" (no ENABLE ROW LEVEL SECURITY, "+info.module+")")
		case !info.hasForce:
			missing = append(missing, name+" (ENABLE but no FORCE, "+info.module+")")
		}
	}
	sort.Strings(missing)

	assert.Emptyf(t, missing,
		"these tables declare tenant_id but do not FORCE row level security: %v\n"+
			"ENABLE alone exempts the table owner, and app_migrator owns every table here "+
			"(ADR-SEC-002). Add `ALTER TABLE <t> FORCE ROW LEVEL SECURITY;` to the migration, "+
			"or record a reviewed exception in rlsExempt with the reason.", missing)

	var stale []string
	for name := range rlsExempt {
		if _, ok := tables[name]; !ok || dropped[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	assert.Emptyf(t, stale, "rlsExempt lists tables that no longer exist: %v — delete the stale exceptions", stale)
}

// stripSQLComments removes `-- …` line comments so a table name mentioned in
// prose (every migration here carries a long Turkish rationale header) cannot
// be mistaken for a statement.
func stripSQLComments(sql string) string {
	lines := strings.Split(sql, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
