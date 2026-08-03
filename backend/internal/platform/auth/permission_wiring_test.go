package auth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// What this file guards against (docs/lessons-from-b2b.md, item 1 "Wiring
// audit" — the OPA bullet: "Embedded OPA gerçekten route middleware
// zincirinde mi, yoksa sadece init mi ediliyor?").
//
// In the sibling b2b repo, Casbin RBAC was initialised and policies were
// seeded into the database, but the enforcement middleware was wired to
// zero routes: the rules existed on paper and were enforced nowhere. This
// repo seeds role_permissions rows the exact same way
// (backend/migrations/identity/000006_seed_system_roles.up.sql) and nothing
// previously verified that every seeded (resource, action) pair actually
// has a corresponding OPA rule that a holder of the role can exercise.
//
// TestSeedPermissions_WiringRegistryIsComplete is that verification. It:
//
//  1. Parses every (role_id, resource, action) row out of the live seed
//     migration SQL (the wildcard ('*','*') manager row is excluded — it is
//     an intentional bypass covered by the `has_role("manager")` rule at
//     the top of authz.rego, not a per-resource wiring question).
//  2. Requires that the resulting set of distinct (resource, action) pairs
//     is EXACTLY the set of keys in permissionWiringRegistry below — no
//     more, no less. A newly seeded permission that nobody has classified
//     yet fails this test immediately, by name.
//  3. For every registry entry, calls the real OPA engine (the actual
//     compiled configs/opa/bundles/authz.rego, not a text/grep proxy) with
//     a principal holding one of the roles the seed migration actually
//     grants that pair to, and asserts the decision matches the entry's
//     Wired flag:
//       - Wired: true  -> engine.Decide(...).Allow must be true today.
//         If this goes false, something that used to be enforced no longer
//         is — a real regression, not documentation drift.
//       - Wired: false -> engine.Decide(...).Allow must be false today.
//         If this goes true, the gap has been closed in code and this
//         baseline entry is stale: delete it from the registry (do NOT
//         leave it as dead weight — a baseline that only ever grows is
//         exactly how this class of drift rots, see the task that added
//         this file).
//
// This test intentionally does NOT try to fix any of the baseline gaps. It
// exists so gaps are an explicit, reviewed, shrinking-or-growing-on-purpose
// list instead of silence.
// ---------------------------------------------------------------------------

// seedMigrationPath is relative to this package's directory, matching the
// relative BundlePath convention already used by newTestEngine in
// opa_test.go ("../../../configs/opa/bundles" -> backend/configs/...).
const seedMigrationPath = "../../../migrations/identity/000006_seed_system_roles.up.sql"

// identityMigrationsGlob covers every identity up-migration, because grants are
// not confined to the original seed file (identity/000014 added some).
const identityMigrationsGlob = "../../../migrations/identity/*.up.sql"

// seedRoleUUIDs mirrors systemRoleUUID's table (opa_test.go) but keyed the
// other way around: UUID string -> system_key. Both tables must be kept in
// sync with identity/000006_seed_system_roles.up.sql by construction, since
// this one is only ever used to label rows parsed straight out of that file.
var seedRoleUUIDs = map[string]string{
	"00000001-0000-0000-0000-000000000001": "cashier",
	"00000001-0000-0000-0000-000000000002": "shift_manager",
	"00000001-0000-0000-0000-000000000003": "driver",
	"00000001-0000-0000-0000-000000000004": "kitchen",
	"00000001-0000-0000-0000-000000000005": "bar",
	"00000001-0000-0000-0000-000000000006": "manager",
	// warehouse is seeded separately, in identity/000010_seed_warehouse_role.
	// Its grants were invisible to this guard until the parser was widened to
	// scan every identity up-migration rather than only 000006.
	"00000001-0000-0000-0000-000000000007": "warehouse",
}

// permissionPair is a (resource, action) column pair as seeded into
// role_permissions.
type permissionPair struct {
	Resource string
	Action   string
}

func (p permissionPair) String() string {
	return fmt.Sprintf("(%s, %s)", p.Resource, p.Action)
}

// wiringEntry is one reviewed classification of a seeded permission pair.
//
// CheckRole must be a system role that the seed migration actually grants
// this pair to (verified by the test itself against the parsed SQL, so the
// registry cannot silently cite a role that doesn't hold the permission).
// CheckAction is the dotted permission string (the shape handlers pass to
// h.permit(...) and OPA matches on input.action) that is asserted allowed
// (Wired: true) or denied (Wired: false) for CheckRole, via a live call
// into the real OPA engine.
type wiringEntry struct {
	Wired       bool
	CheckRole   string
	CheckAction string
	Reason      string
}

// permissionWiringRegistry is the reviewed, one-time-classified mapping from
// every currently-seeded role_permissions (resource, action) pair to its
// actual enforcement status. See the file header for the mechanism.
//
// Wired: true entries are evidence, not aspiration — CheckAction is a real
// permission string found at a real h.permit(...) call site (grep-verified
// against backend/internal/modules/*/http/*.go while this registry was
// built) AND allowed by the real compiled rego bundle for CheckRole.
//
// Wired: false entries are the known, accepted baseline. Each one names the
// work item that will eventually wire it, per the task that introduced this
// file — do not fix them here.
var permissionWiringRegistry = map[permissionPair]wiringEntry{
	// -- checks (pos.check.*) -------------------------------------------
	{"checks", "read"}: {
		Wired: true, CheckRole: "cashier", CheckAction: "pos.check.read",
	},
	{"checks", "create"}: {
		Wired: true, CheckRole: "cashier", CheckAction: "pos.check.open",
		Reason: "seed 'create' == opening a check (pos.check.open).",
	},
	{"checks", "update"}: {
		Wired: true, CheckRole: "cashier", CheckAction: "pos.check.close",
		Reason: "seed 'update' covers the check lifecycle transitions " +
			"pos.check.close / pos.check.cancel; evidenced via close.",
	},
	{"checks", "delete"}: {
		Wired: false, CheckRole: "shift_manager", CheckAction: "pos.check.delete",
		Reason: "no check-deletion feature exists anywhere in the pos module " +
			"(checks are cancelled via pos.check.cancel, never hard-deleted). " +
			"Not planned; would need a product decision before it makes sense to build.",
	},
	{"checks", "approve"}: {
		Wired: false, CheckRole: "shift_manager", CheckAction: "pos.check.approve",
		Reason: "yönetici onayı akışı (manager approval workflow for checks) is " +
			"not yet built. The only 'approve' action wired anywhere is " +
			"inventory.transfer_order.approve — unrelated module.",
	},

	// -- orders (pos.order.*) --------------------------------------------
	{"orders", "read"}: {
		Wired: true, CheckRole: "cashier", CheckAction: "pos.order.read",
		Reason: "wired for cashier/shift_manager/kitchen/bar. NOTE: the " +
			"'driver' role also holds this seed row but authz.rego's " +
			"pos_counter_actions/pos_kitchen_actions any_role sets do not " +
			"include 'driver' — a driver principal is denied pos.order.read " +
			"today despite the seeded grant. Flagged, not fixed (out of scope).",
	},
	{"orders", "create"}: {
		Wired: true, CheckRole: "cashier", CheckAction: "pos.order.place",
		Reason: "seed 'create' == placing an order (pos.order.place).",
	},
	{"orders", "update"}: {
		Wired: true, CheckRole: "cashier", CheckAction: "pos.order.accept",
		Reason: "seed 'update' covers the order status transitions " +
			"pos.order.accept / reject / advance; evidenced via accept.",
	},
	{"orders", "delete"}: {
		Wired: false, CheckRole: "shift_manager", CheckAction: "pos.order.delete",
		Reason: "no order-deletion feature exists; orders move through the " +
			"status machine (reject/cancel-by-status) and are never hard-deleted.",
	},
	{"orders", "approve"}: {
		Wired: false, CheckRole: "shift_manager", CheckAction: "pos.order.approve",
		Reason: "yönetici onayı akışı not yet built — same gap as checks:approve.",
	},

	// -- tables (pos.table.*) --------------------------------------------
	{"tables", "read"}: {
		Wired: true, CheckRole: "cashier", CheckAction: "pos.table.read",
	},
	{"tables", "create"}: {
		Wired: true, CheckRole: "shift_manager", CheckAction: "pos.table.manage",
		Reason: "table/zone create+update+status-change collapse into a single " +
			"pos.table.manage action in code (see pos/http/handler.go route comment).",
	},
	{"tables", "update"}: {
		Wired: true, CheckRole: "shift_manager", CheckAction: "pos.table.manage",
	},
	{"tables", "delete"}: {
		Wired: false, CheckRole: "shift_manager", CheckAction: "pos.table.delete",
		Reason: "no table-deletion feature exists — no DeleteTable service " +
			"method, no DELETE route. Only create/update/status-change are " +
			"implemented, both under pos.table.manage.",
	},

	// -- catalog (read-only for POS-facing roles) -------------------------
	{"catalog", "read"}: {
		Wired: true, CheckRole: "cashier", CheckAction: "catalog.product.read",
	},

	// -- payment ------------------------------------------------------------
	{"payment", "create"}: {
		Wired: true, CheckRole: "cashier", CheckAction: "payment.sale.register",
	},
	{"payment", "read"}: {
		Wired: true, CheckRole: "shift_manager", CheckAction: "payment.payment.read",
	},

	// -- shifts (kasa oturumu / cash session, ADR-DATA-008) -----------------
	// Module ownership landed in `payment`, not `pos` (see the ADR's "Modul
	// sahipligi" section: the session's content is entirely money and its
	// close guard needs fiscal-submission state, both payment-owned) — hence
	// payment.cash_session.* rather than the pos.shift.* name this registry
	// guessed before that decision was made.
	{"shifts", "read"}: {
		Wired: true, CheckRole: "cashier", CheckAction: "payment.cash_session.read",
	},
	// CheckRole is deliberately "cashier" for the write actions: identity/000014
	// widened shifts:create+update from shift_manager-only to the cashier who
	// actually counts the drawer. Asserting the cashier proves the weaker of the
	// two holders is allowed — if that passes, shift_manager necessarily does too.
	{"shifts", "create"}: {
		Wired: true, CheckRole: "cashier", CheckAction: "payment.cash_session.open",
		Reason: "seed 'create' == opening a cash session (payment.cash_session.open).",
	},
	{"shifts", "update"}: {
		Wired: true, CheckRole: "cashier", CheckAction: "payment.cash_session.close",
		Reason: "seed 'update' covers the cash session lifecycle mutations " +
			"payment.cash_session.movement / payment.cash_session.submit_closing / " +
			"payment.cash_session.close / payment.cash_session.join / " +
			"payment.cash_session.switch; evidenced via close. " +
			"payment.cash_session.pin_reset is the one write verb in this family " +
			"NOT covered by this grant — it stays shift_manager-only " +
			"(ADR-DATA-008 PIN akışı §2: a cashier may never clear a PIN).",
	},

	// -- staff --------------------------------------------------------------
	{"staff", "read"}: {
		Wired: false, CheckRole: "shift_manager", CheckAction: "hrcore.employee.read",
		Reason: "hrcore.employee.read exists as a real permit(...) call site " +
			"(hr-core/http/handler.go) and a real OPA action, but authz.rego " +
			"authorizes it for 'manager' only (back-office module, per the " +
			"top-of-file comment) — shift_manager is denied. No shift_manager " +
			"staff-read flow has been designed yet.",
	},

	// -- reports --------------------------------------------------------------
	{"reports", "read"}: {
		Wired: false, CheckRole: "shift_manager", CheckAction: "reports.summary.read",
		Reason: "no reports module exists yet at all (no permit(...) call site, " +
			"no OPA action). 'reports.summary.read' is a placeholder name for " +
			"the watch check; see shifts/read's caveat about guessed names.",
	},

	// -- inventory ------------------------------------------------------------
	{"inventory", "read"}: {
		Wired: false, CheckRole: "kitchen", CheckAction: "inventory.level.read",
		Reason: "inventory.level.read IS a real, enforced permission (real " +
			"permit(...) call sites in inventory/http/handler.go, real OPA " +
			"rule inventory_management_actions) — but that rule is manager/" +
			"warehouse only (ADR-DATA-005 İlke 4). The seed migration also " +
			"grants this exact (resource, action) pair to kitchen and bar; " +
			"OPA denies both today. This is a role-scope mismatch rather than " +
			"a zero-enforcement gap, but it still means the seeded grant is " +
			"unusable for its actual holders, so it is tracked here the same way.",
	},

	// -- warehouse (identity/000010_seed_warehouse_role) -----------------------
	// These 14 pairs were invisible to this guard until the parser was widened
	// to scan every identity up-migration instead of only 000006 — the whole
	// warehouse role had never been checked. All of them resolve to actions in
	// authz.rego's inventory_management_actions set, which grants `warehouse`
	// wholesale (ADR-DATA-005 İlke 4), so all are genuinely enforced.
	//
	// The seed's coarse `update` maps to the lifecycle verb a depo operator
	// actually performs, matching the {checks,update}/{orders,update} pattern
	// above: transfer_orders -> submit, shipments -> advance.
	{"stock_items", "read"}:     {Wired: true, CheckRole: "warehouse", CheckAction: "inventory.stock_item.read"},
	{"stock_items", "create"}:   {Wired: true, CheckRole: "warehouse", CheckAction: "inventory.stock_item.create"},
	{"stock_items", "update"}:   {Wired: true, CheckRole: "warehouse", CheckAction: "inventory.stock_item.update"},
	{"warehouses", "read"}:      {Wired: true, CheckRole: "warehouse", CheckAction: "inventory.warehouse.read"},
	{"warehouses", "update"}:    {Wired: true, CheckRole: "warehouse", CheckAction: "inventory.warehouse.update"},
	{"stock_levels", "read"}:    {Wired: true, CheckRole: "warehouse", CheckAction: "inventory.level.read"},
	{"stock_movements", "read"}: {Wired: true, CheckRole: "warehouse", CheckAction: "inventory.movement.read"},
	{"stock_movements", "create"}: {
		Wired: true, CheckRole: "warehouse", CheckAction: "inventory.movement.create",
	},
	{"transfer_orders", "read"}: {Wired: true, CheckRole: "warehouse", CheckAction: "inventory.transfer_order.read"},
	{"transfer_orders", "create"}: {
		Wired: true, CheckRole: "warehouse", CheckAction: "inventory.transfer_order.create",
	},
	{"transfer_orders", "update"}: {
		Wired: true, CheckRole: "warehouse", CheckAction: "inventory.transfer_order.submit",
		Reason: "seed 'update' covers the BTO lifecycle verbs (submit/approve/" +
			"reject/cancel/fulfil); evidenced via submit.",
	},
	{"shipments", "read"}:   {Wired: true, CheckRole: "warehouse", CheckAction: "inventory.shipment.read"},
	{"shipments", "create"}: {Wired: true, CheckRole: "warehouse", CheckAction: "inventory.shipment.create"},
	{"shipments", "update"}: {
		Wired: true, CheckRole: "warehouse", CheckAction: "inventory.shipment.advance",
		Reason: "seed 'update' covers advance/receive/cancel; evidenced via advance.",
	},
}

// seedPermissionRowRe matches a single VALUES row inside an
// "INSERT INTO role_permissions (...) VALUES (...), (...), ... ON CONFLICT
// DO NOTHING;" statement: ('<role-uuid>', NULL, '<resource>', '<action>').
var seedPermissionRowRe = regexp.MustCompile(
	`\(\s*'([0-9a-fA-F-]{36})'\s*,\s*NULL\s*,\s*'([^']+)'\s*,\s*'([^']+)'\s*\)`,
)

// seedRolePermissionBlockRe isolates each "INSERT INTO role_permissions ...;"
// statement in the migration file. It must NOT also match
// "INSERT INTO role_field_policies ...;" (same 4-column-literal shape, but a
// (resource, field) list, not (resource, action)) — matching that table too
// would corrupt the parsed pair set with rows like ('checks', 'id').
var seedRolePermissionBlockRe = regexp.MustCompile(`(?s)INSERT INTO role_permissions\b.*?;`)

// parsedSeedRow is one row parsed straight out of the seed migration SQL.
type parsedSeedRow struct {
	RoleKey  string // system_key, resolved via seedRoleUUIDs
	Resource string
	Action   string
}

// seedRevocationRe catches an up-migration that REMOVES grants. The parser
// below is purely additive — it unions every INSERT it finds — so a revocation
// would leave it believing a role still holds a pair it no longer has. Rather
// than model deletion (and get it subtly wrong), fail loudly and make whoever
// wrote the revocation extend this parser deliberately.
var seedRevocationRe = regexp.MustCompile(`(?is)(DELETE\s+FROM|TRUNCATE)\s+role_permissions\b`)

// parseSeedRolePermissions reads and parses the role_permissions INSERT
// statements out of EVERY identity up-migration, not just the original seed.
// Scanning only 000006 would make this guard blind to any later migration that
// grants a permission — precisely the drift it exists to catch (identity/000014
// was the first such migration).
//
// It is deliberately a small regex scan, not a SQL parser — proportionate for
// fixed, hand-written migration files (the same tradeoff
// scripts/lint_sql_rules.sh makes for its own textual invariants). The cost is
// that grants must be written as literal (role_id, NULL, resource, action)
// tuples to be visible here; 000014 documents that requirement at its INSERT.
func parseSeedRolePermissions(t *testing.T) []parsedSeedRow {
	t.Helper()

	files, err := filepath.Glob(identityMigrationsGlob)
	require.NoError(t, err, "glob identity migrations %s", identityMigrationsGlob)
	require.NotEmpty(t, files, "no identity up-migrations matched %s", identityMigrationsGlob)
	sort.Strings(files)

	var rows []parsedSeedRow
	for _, file := range files {
		raw, err := os.ReadFile(file)
		require.NoError(t, err, "read migration %s", file)
		text := string(raw)

		blocks := seedRolePermissionBlockRe.FindAllString(text, -1)
		if len(blocks) == 0 {
			continue
		}

		require.False(t, seedRevocationRe.MatchString(text),
			"%s revokes role_permissions rows; parseSeedRolePermissions only unions "+
				"INSERTs and would report grants that no longer exist. Teach it to "+
				"model revocation before shipping this migration.", file)

		for _, block := range blocks {
			for _, m := range seedPermissionRowRe.FindAllStringSubmatch(block, -1) {
				roleUUID, resource, action := m[1], m[2], m[3]
				roleKey, ok := seedRoleUUIDs[roleUUID]
				require.True(t, ok,
					"%s: role_permissions row references role_id %s which is not in "+
						"seedRoleUUIDs — a new system role was seeded and this test's "+
						"role table needs updating", file, roleUUID)
				rows = append(rows, parsedSeedRow{RoleKey: roleKey, Resource: resource, Action: action})
			}
		}
	}
	require.NotEmpty(t, rows, "parsed zero role_permissions rows out of %s — regex likely stale", identityMigrationsGlob)
	return rows
}

// TestSeedPermissions_WiringRegistryIsComplete is the guard described in the
// file header. See there for the full mechanism.
func TestSeedPermissions_WiringRegistryIsComplete(t *testing.T) {
	rows := parseSeedRolePermissions(t)

	// Group parsed rows by pair, excluding the manager wildcard ('*','*'):
	// that row is an intentional global bypass (has_role("manager") at the
	// top of authz.rego), not a per-resource wiring question this registry
	// is meant to answer.
	holders := map[permissionPair]map[string]bool{}
	for _, row := range rows {
		if row.Resource == "*" && row.Action == "*" {
			continue
		}
		pair := permissionPair{Resource: row.Resource, Action: row.Action}
		if holders[pair] == nil {
			holders[pair] = map[string]bool{}
		}
		holders[pair][row.RoleKey] = true
	}

	// --- Completeness: parsed pairs == registry keys, exactly. -------------
	var missingFromRegistry, staleInRegistry []string

	for pair := range holders {
		if _, ok := permissionWiringRegistry[pair]; !ok {
			missingFromRegistry = append(missingFromRegistry, pair.String())
		}
	}
	for pair := range permissionWiringRegistry {
		if _, ok := holders[pair]; !ok {
			staleInRegistry = append(staleInRegistry, pair.String())
		}
	}
	sort.Strings(missingFromRegistry)
	sort.Strings(staleInRegistry)

	if len(missingFromRegistry) > 0 {
		t.Errorf("role_permissions seeds pair(s) not classified in permissionWiringRegistry "+
			"(backend/internal/platform/auth/permission_wiring_test.go): %v\n"+
			"Add an entry classifying whether it is actually enforced (Wired: true, with real "+
			"evidence) or a known gap (Wired: false, with a reason) before this can pass — "+
			"see docs/lessons-from-b2b.md item 1.", missingFromRegistry)
	}
	if len(staleInRegistry) > 0 {
		t.Errorf("permissionWiringRegistry has entries for pair(s) no longer present in "+
			"%s: %v\nRemove the stale entries.", seedMigrationPath, staleInRegistry)
	}
	if len(missingFromRegistry) > 0 || len(staleInRegistry) > 0 {
		return // no point evaluating OPA against a registry known to be out of sync
	}

	// --- Per-entry OPA verification. ----------------------------------------
	engine := newTestEngine(t)
	ctx := context.Background()

	pairs := make([]permissionPair, 0, len(permissionWiringRegistry))
	for pair := range permissionWiringRegistry {
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Resource != pairs[j].Resource {
			return pairs[i].Resource < pairs[j].Resource
		}
		return pairs[i].Action < pairs[j].Action
	})

	for _, pair := range pairs {
		entry := permissionWiringRegistry[pair]

		// Sanity-check the registry itself: CheckRole must be a role the
		// seed migration actually grants this pair to, so a wiring entry
		// can never quietly test the wrong principal.
		if !holders[pair][entry.CheckRole] {
			t.Errorf("%s: registry's CheckRole %q is not among the roles the seed "+
				"migration actually grants this pair to (%v) — fix the registry entry",
				pair, entry.CheckRole, sortedKeys(holders[pair]))
			continue
		}

		roleUUID, err := uuid.Parse(reverseRoleUUID(entry.CheckRole))
		require.NoError(t, err, "%s: resolve role UUID for %q", pair, entry.CheckRole)

		principal := Principal{
			PersonID: uuid.New(),
			Ctx:      ContextStaff,
			TenantID: uuid.New(),
			BranchID: uuid.New(),
			RoleIDs:  []uuid.UUID{roleUUID},
		}

		decision, err := engine.Decide(ctx, entry.CheckAction, principal)
		require.NoError(t, err, "%s: OPA decide(%s) for role %s", pair, entry.CheckAction, entry.CheckRole)

		switch {
		case entry.Wired && !decision.Allow:
			t.Errorf("%s: registry claims Wired=true (action %q, role %q) but OPA denies it "+
				"today — enforcement regressed since this registry entry was written. "+
				"Either the permission string changed or the rego rule was narrowed/removed. "+
				"Reason on file: %s", pair, entry.CheckAction, entry.CheckRole, entry.Reason)
		case !entry.Wired && decision.Allow:
			t.Errorf("%s: baseline entry (Wired=false, action %q, role %q) is now ALLOWED by "+
				"OPA — this gap has been closed. Remove this entry from permissionWiringRegistry "+
				"(do not leave a wired permission in the unwired baseline). Reason on file was: %s",
				pair, entry.CheckAction, entry.CheckRole, entry.Reason)
		}
	}
}

// reverseRoleUUID looks up the UUID string for a system role key. Panics on
// an unknown key since that only happens from a typo in this file's own
// registry, which should fail loudly during test authoring, not silently.
func reverseRoleUUID(roleKey string) string {
	for id, key := range seedRoleUUIDs {
		if key == roleKey {
			return id
		}
	}
	panic("permission_wiring_test.go: unknown role key " + roleKey)
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
