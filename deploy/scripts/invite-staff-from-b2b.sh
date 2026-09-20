#!/usr/bin/env bash
# b2b personel → Online Menu davet komutları (YALNIZ ÜRETİR, HİÇBİRİNİ ÇALIŞTIRMAZ).
#
# Kaynak CSV (git dışı!): full_name,email,b2b_role,b2b_branch_code
#   b2b'den (yalnız SELECT):
#   \copy (SELECT btrim(u.full_name), lower(btrim(u.email)), u.role, COALESCE(b.code,'')
#          FROM users u LEFT JOIN branches b ON b.id=u.branch_id
#          WHERE u.deleted_at IS NULL AND u.is_active) TO stdout WITH CSV HEADER
#
#   STAFF_CSV=deploy/b2b-staff.local.csv TENANT_ID=<uuid> API_URL=https://api.diverstreetfood.com \
#   BRANCH_MAP=<dosya: "slug|uuid" satırları, `SELECT slug, id FROM branches`> \
#   SKIP_EMAILS="a@x.com b@y.com" \
#     deploy/scripts/invite-staff-from-b2b.sh > deploy/b2b-staff.local.commands.sh
#
# Üretilen dosyadaki her satır `POST /v1/identity/{tid}/staff` çağıran bir curl'dür; TOKEN (Yönetici
# bağlam token'ı) ortam değişkeninden okunur. İnceleyip elle çalıştırın. Rol eşlemesi (docs/b2b-import-plan.md §6):
#   admin→Yönetici(şubesiz) · branch→Shift Müdürü · branch_staff→Kasiyer · manufacturing→Depo · driver→Şoför
#   (imalat/şoför şubesi olmayanlar İmalat Merkezi'ne) · auditor→ATLANIR. Şube-kapsamlı rollerde branch_id zorunludur (SEC-005).
set -euo pipefail
: "${STAFF_CSV:?}" "${TENANT_ID:?}" "${API_URL:?}" "${BRANCH_MAP:?}"

python3 - "$STAFF_CSV" "$TENANT_ID" "$API_URL" "$BRANCH_MAP" "${SKIP_EMAILS:-}" <<'PY'
import csv, json, shlex, sys

csv_path, tenant, api, map_path, skip = sys.argv[1:6]
skip = {e.lower() for e in skip.split()}
branch = dict(l.strip().split("|", 1) for l in open(map_path, encoding="utf-8") if "|" in l)
slug_of = {"ADA": "adapazari", "IZM": "izmit", "KRK": "kirkpinar", "SRD": "serdivan", "IMALAT": "imalat-merkezi-serdivan"}
role = {  # b2b role → (OM role id, label, needs branch, fallback branch code)
    "admin": ("00000001-0000-0000-0000-000000000006", "Yönetici", False, None),
    "branch": ("00000001-0000-0000-0000-000000000002", "Shift Müdürü", True, None),
    "branch_staff": ("00000001-0000-0000-0000-000000000001", "Kasiyer", True, None),
    "manufacturing": ("00000001-0000-0000-0000-000000000007", "Depo", True, "IMALAT"),
    "driver": ("00000001-0000-0000-0000-000000000003", "Şoför", True, "IMALAT"),
}
print("#!/usr/bin/env bash\n# ÜRETİLDİ — incelemeden çalıştırmayın. TOKEN = Yönetici bağlam token'ı.\nset -euo pipefail\n: \"${TOKEN:?}\"")
for r in csv.DictReader(open(csv_path, encoding="utf-8")):
    email, kind = r["email"].strip().lower(), r["b2b_role"]
    if kind not in role:
        print(f"# ATLANDI ({kind}): {r['full_name']}")
        continue
    if email in skip:
        print(f"# ATLANDI (OM'de zaten var): {r['full_name']}")
        continue
    rid, label, needs_branch, fallback = role[kind]
    code = r["b2b_branch_code"] or fallback
    body = {"full_name": r["full_name"], "email": email, "role_id": rid}
    if needs_branch:
        if not code or slug_of[code] not in branch:
            sys.exit(f"şube çözülemedi: {r['full_name']} ({kind})")
        body["branch_id"] = branch[slug_of[code]]
    print(f"# {label}" + (f" — {slug_of[code]}" if needs_branch else " — zincir"))
    print(f"curl -fsS -X POST {shlex.quote(api + '/v1/identity/' + tenant + '/staff')} "
          f"-H \"Authorization: Bearer $TOKEN\" -H 'Content-Type: application/json' -d {shlex.quote(json.dumps(body, ensure_ascii=False))}")
PY
