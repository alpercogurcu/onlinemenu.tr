#!/usr/bin/env bash
#
# lint_sql_rules.sh -- enforces the SQL invariants from CLAUDE.md / delta-v2.md
# Bolum 3.7 that go-arch-lint and forbidigo cannot express, because they are
# properties of SQL text (Go string literals and raw migration SQL), not Go
# AST shapes.
#
# Rules enforced (see docs/lessons-from-b2b.md item 1 -- a rule that is only
# written in prose is not enforced; this script is the enforcement):
#
#   (a) Bare 'SET app.*' (session-scoped GUC) is forbidden anywhere.
#       Only 'SET LOCAL app.*' is safe with pgBouncer transaction-mode pooling
#       (ADR-SEC-001, ADR-SEC-002) -- a bare SET leaks the tenant context to
#       whichever request pgBouncer hands the pooled connection to next.
#
#   (b) 'INSERT ... ON CONFLICT ... DO UPDATE' targeting a '*_outbox' table
#       is forbidden. Outbox events are immutable; consumers/producers may
#       only ever 'DO NOTHING' on conflict (ADR-DATA-002).
#
#   (c) 'UPDATE <module>_outbox SET ... payload = ...' is forbidden. Mutating
#       an already-recorded event payload violates event immutability
#       (ADR-DATA-002) -- corrections must be a new event, never an edit.
#
# Scope: Go sources under backend/internal/**/*.go (SQL lives in backtick
# raw string literals) and raw SQL under backend/migrations/**/*.sql.
#
# This is a textual heuristic, not a SQL parser: to keep false positives near
# zero it scopes each check to one "chunk" at a time -- a single Go backtick
# string literal, or a single semicolon-delimited SQL statement -- so a
# violation in one query can never be inferred from unrelated text elsewhere
# in the file. Parameterized table names built with fmt.Sprintf (e.g.
# 'UPDATE %s SET ...') are intentionally out of scope: they cannot be
# resolved statically, and are reviewed by hand (see internal/platform/outbox,
# which this script does not need to flag because it only ever sets
# claimed_at / processed_at / retry_count, never payload).
#
# Usage: backend/scripts/lint_sql_rules.sh
# Exit status: 0 when clean, 1 when any violation is found. Every violation
# is printed as "path:line: message" on stdout.

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

if ! command -v perl >/dev/null 2>&1; then
  echo "lint_sql_rules.sh: perl is required but not found in PATH" >&2
  exit 2
fi

# Built via a read loop (not "mapfile") for compatibility with bash 3.2
# (macOS ships bash 3.2; mapfile/readarray require bash 4+).
files=()
while IFS= read -r f; do
  files+=("$f")
done < <(
  { find internal -type f -name '*.go' ! -name '*_test.go'; find migrations -type f -name '*.sql'; } 2>/dev/null | sort
)

if [ "${#files[@]}" -eq 0 ]; then
  echo "lint_sql_rules.sh: no files found to scan (internal/**/*.go, migrations/**/*.sql)" >&2
  exit 2
fi

set +e
output="$(perl - "${files[@]}" <<'PERL_EOF'
use strict;
use warnings;

my $violations = 0;
my $BACKTICK = chr(96);

for my $file (@ARGV) {
    open(my $fh, '<', $file) or die "cannot open $file: $!";
    local $/;
    my $content = <$fh>;
    close($fh);

    my @chunks; # array of [start_line, text]

    if ($file =~ /\.go$/) {
        # SQL in this codebase always lives in backtick raw string literals.
        # Splitting on backticks isolates each SQL string from surrounding Go
        # code and from unrelated SQL strings elsewhere in the same file.
        my @parts = split(/\Q$BACKTICK\E/, $content, -1);
        my $line = 1;
        for (my $i = 0; $i < @parts; $i++) {
            push @chunks, [$line, $parts[$i]] if $i % 2 == 1;
            my $nl = () = $parts[$i] =~ /\n/g;
            $line += $nl;
        }
    } else {
        # Raw SQL migration file: scope each check to one statement.
        my @parts = split(/;/, $content, -1);
        my $line = 1;
        for my $part (@parts) {
            push @chunks, [$line, $part];
            my $nl = () = $part =~ /\n/g;
            $line += $nl;
        }
    }

    for my $chunk (@chunks) {
        my ($start_line, $text) = @$chunk;

        # Rule (a): bare "SET app.*" -- "SET LOCAL app.*" is exempt.
        while ($text =~ /\bSET\s+(?!LOCAL\b)app\./gi) {
            my $pos = pos($text) - length($&);
            my $rel = () = substr($text, 0, $pos) =~ /\n/g;
            print "$file:" . ($start_line + $rel)
                . ": bare 'SET app.*' forbidden -- use 'SET LOCAL app.*' (pgBouncer transaction-mode safety, ADR-SEC-001/002)\n";
            $violations++;
        }

        # Rule (b): "ON CONFLICT ... DO UPDATE" on a "*_outbox" table.
        if ($text =~ /\w*_outbox\b/i && $text =~ /ON\s+CONFLICT/i && $text =~ /DO\s+UPDATE/i) {
            if ($text =~ /ON\s+CONFLICT/gi) {
                my $pos = pos($text) - length($&);
                my $rel = () = substr($text, 0, $pos) =~ /\n/g;
                print "$file:" . ($start_line + $rel)
                    . ": 'ON CONFLICT ... DO UPDATE' on an outbox table forbidden -- outbox rows are immutable, use 'DO NOTHING' (ADR-DATA-002)\n";
                $violations++;
            }
        }

        # Rule (c): "UPDATE <module>_outbox ... SET ... payload = ...".
        if ($text =~ /UPDATE\s+\w*_outbox\b/i && $text =~ /\bpayload\s*=/i) {
            if ($text =~ /\bpayload\s*=/gi) {
                my $pos = pos($text) - length($&);
                my $rel = () = substr($text, 0, $pos) =~ /\n/g;
                print "$file:" . ($start_line + $rel)
                    . ": 'UPDATE ..._outbox SET payload' forbidden -- outbox payload is immutable once written, publish a new event instead (ADR-DATA-002)\n";
                $violations++;
            }
        }
    }
}

exit($violations > 0 ? 1 : 0);
PERL_EOF
)"
status=$?
set -e

if [ -n "$output" ]; then
  echo "$output"
fi

if [ "$status" -ne 0 ]; then
  echo "" >&2
  echo "lint_sql_rules.sh: SQL invariant violation(s) found (see above)." >&2
  exit 1
fi

echo "lint_sql_rules.sh: OK -- no SQL invariant violations found."
exit 0
