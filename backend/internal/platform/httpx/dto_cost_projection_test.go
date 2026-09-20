package httpx_test

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
// Cost-field projection guard (docs/lessons-from-b2b.md item 2, first bullet:
// "Alan yokluğu, sıfır değeri değil").
//
// b2b's headline leak was a branch-scoped user seeing `cost_price_tl`. The
// root cause was not a missing check: it was that hiding a financial field
// was OPT-IN (a helper every handler had to remember to call), so the N-th
// new DTO simply never called it and the cost shipped.
//
// This repo's design is the opposite — cost lives on the domain struct and is
// simply ABSENT from the response type (inventory/http/handler.go
// levelResponse has no cost field at all; stock_item_handler.go has a second,
// narrower type restrictedStockItemResponse rather than a blanked-out copy of
// the full one). That design is correct, but nothing held it there: adding
// `LastUnitCost *float64 \`json:"last_unit_cost"\`` to levelResponse would
// have compiled, passed every existing test and leaked.
//
// This test is the thing that holds it. It parses every json struct tag in
// every module's http layer and fails if a cost-bearing key appears anywhere
// that has not been explicitly reviewed and allowlisted below. A new leak is
// a failing test with the exact file and key named; a legitimate new
// cost-bearing endpoint is one allowlist line plus the sentence explaining
// which OPA action gates it.
//
// Deliberately NOT done here (and it is a decision, not an omission): we do
// not add an `inventory.cost.read` permission whose absence blanks the field
// inside a shared DTO. That is precisely b2b's opt-in field-hiding pattern
// rebuilt one layer up. Separate response types are enforced by the compiler;
// a conditional projection is enforced by whoever remembers to call it.
// ---------------------------------------------------------------------------

// costKeySubstrings mark a JSON key as carrying procurement/cost information
// wherever they appear in the key name.
var costKeySubstrings = []string{
	"cost",
	"margin",
	"landed",
	"purchase_price",
	"supplier_price",
	"buy_price",
}

// costKeysExact are matched whole, not as substrings, because the sale-side
// keys they would otherwise swallow are public by design: pos/storefront emit
// "unit_price_amount" / "unit_price_minor" for the price the guest is charged
// on a line, and billing repeats it on the invoice. Those are the customer's
// own number. Procurement's "unit_price" — what the business PAID — is the
// one that must stay behind a manager/warehouse-only route.
var costKeysExact = []string{
	"unit_price",
	"unit_cost",
}

// allowedCostKeys maps "<module>/http/<file>.go" -> the cost keys that file is
// permitted to serialize, each justified by the OPA action that gates the
// route rendering it. Every action listed here lives in authz.rego's
// inventory_management_actions set, i.e. manager + warehouse only; no
// counter-facing role (cashier/waiter/kitchen/bar) and no guest can reach
// them. See inventory/http/authz_policy_test.go
// (TestAuthz_InventoryManagement_BranchRolesDenied) for that half of the
// proof, and storefront/http/menu_dto_test.go for the guest half.
var allowedCostKeys = map[string][]string{
	// inventory.purchase_receipt.create / .read — elden alım fişi
	// (ADR-DATA-007 karar 3): the purchase price IS the payload of this
	// endpoint; hiding it would make the feature meaningless.
	"inventory/http/purchase_receipt_handler.go": {"unit_price"},
	// inventory.transfer_order.* — the HQ→şube transfer (sale) price set by
	// the source branch at approve time (ADR-DATA-006 eklenti).
	"inventory/http/transfer_order_handler.go": {"unit_price"},
	// inventory.shipment.* — the transfer price frozen onto the shipment
	// line at create time (ADR-DATA-006 eklenti).
	"inventory/http/shipment_handler.go": {"unit_price"},
}

var jsonTagRe = regexp.MustCompile(`json:"([^"]*)"`)

// moduleTransportDirs returns every internal/modules/<mod>/{http,ws} directory.
// ws/ is included because it is the second place JSON leaves this process:
// pos/ws serializes order and check snapshots to the kitchen display, whose
// operator is a restricted role. A scan that only covered http/ would declare
// the kitchen board clean without ever looking at it.
func moduleTransportDirs(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..", "modules")
	entries, err := os.ReadDir(root)
	require.NoError(t, err, "internal/modules must be readable from platform/httpx")

	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		for _, layer := range []string{"http", "ws"} {
			dir := filepath.Join(root, e.Name(), layer)
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				dirs = append(dirs, dir)
			}
		}
	}
	require.NotEmpty(t, dirs, "no module transport packages found — the path walk is broken, not the codebase")
	return dirs
}

// relKey turns ".../internal/modules/inventory/http/handler.go" into the
// "inventory/http/handler.go" form allowedCostKeys is written in.
func relKey(path string) string {
	p := filepath.ToSlash(path)
	idx := strings.Index(p, "modules/")
	if idx < 0 {
		return p
	}
	return p[idx+len("modules/"):]
}

func isCostKey(key string) bool {
	lower := strings.ToLower(key)
	for _, pat := range costKeySubstrings {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	for _, exact := range costKeysExact {
		if lower == exact {
			return true
		}
	}
	return false
}

// TestDTOs_DoNotLeakCostFields is the enforcement described at the top of this
// file: a cost-bearing JSON key may only appear in an allowlisted handler.
func TestDTOs_DoNotLeakCostFields(t *testing.T) {
	seen := map[string]map[string]bool{}

	for _, dir := range moduleTransportDirs(t) {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		require.NoError(t, err)

		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			src, err := os.ReadFile(file) //nolint:gosec // test-only read of repo source
			require.NoError(t, err)

			key := relKey(file)
			for _, m := range jsonTagRe.FindAllStringSubmatch(string(src), -1) {
				name, _, _ := strings.Cut(m[1], ",")
				name = strings.TrimSpace(name)
				if name == "" || name == "-" || !isCostKey(name) {
					continue
				}
				if seen[key] == nil {
					seen[key] = map[string]bool{}
				}
				seen[key][name] = true

				assert.Containsf(t, allowedCostKeys[key], name,
					"%s serializes cost-bearing JSON key %q. Response DTOs must not carry "+
						"procurement cost (docs/lessons-from-b2b.md item 2). If this endpoint "+
						"legitimately needs it, add it to allowedCostKeys with the manager/"+
						"warehouse-only OPA action that gates the route.", key, name)
			}
		}
	}

	// A stale allowlist entry is how this guard rots: the exception outlives
	// the endpoint and silently pre-approves the next leak in that file.
	for key, keys := range allowedCostKeys {
		for _, name := range keys {
			assert.Truef(t, seen[key][name],
				"allowedCostKeys lists %q for %s but that key no longer exists — delete the stale exception",
				name, key)
		}
	}
}

// TestNoCostKeysOutsideInventory is the blunt companion assertion: whatever
// the allowlist says, no module other than inventory may serialize cost at
// all. Catalog (which cashier/kitchen/bar can read) and pos are the two
// surfaces b2b leaked through; this pins them shut by module, so a future
// "just add cost to the product DTO for the POS margin badge" cannot be
// waved through with a one-line allowlist edit.
func TestNoCostKeysOutsideInventory(t *testing.T) {
	var offenders []string
	for key := range allowedCostKeys {
		if !strings.HasPrefix(key, "inventory/") {
			offenders = append(offenders, key)
		}
	}
	sort.Strings(offenders)
	assert.Emptyf(t, offenders,
		"cost keys are allowlisted outside the inventory module (%v). Inventory is the only "+
			"module whose read actions are manager/warehouse-only; every other module's http "+
			"layer is reachable by a counter-facing role or a guest.", offenders)
}
