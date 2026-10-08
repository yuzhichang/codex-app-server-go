SDK_DIR := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))

# The protocol anchor is a codex *git commit*, not the installed codex CLI: the CLI may be
# behind or ahead of the pinned commit (at the time of writing 0.160.0 is one method behind
# 14c8b777). Point CODEX_SRC at a checkout of github.com/openai/codex.
CODEX_SRC ?= $(HOME)/github.com/openai/codex

.PHONY: sync verify diff-cli coverage conformance conformance-strict build test

# Regenerate vendored schema artifacts + gen/*.json from a codex checkout.
# Deterministic: a no-op re-run must produce an empty `git diff`.
sync:
	python3 $(SDK_DIR)/scripts/codex_schema_surface.py sync --codex-src $(CODEX_SRC)

# CI-safe reconciliation: needs no codex checkout, no `zstandard`, no network. Runs the six
# set assertions over the checked-in gen/*.json + .codex-schema/exports/*.
verify:
	python3 $(SDK_DIR)/scripts/codex_schema_surface.py verify

# Guard: compare a CLI-generated bundle against the pinned anchor and fail on drift.
diff-cli:
	@TMPDIR=$$(mktemp -d) && \
	codex app-server generate-json-schema --out "$$TMPDIR" 2>/dev/null && \
	python3 $(SDK_DIR)/scripts/codex_schema_surface.py diff-cli --bundle "$$TMPDIR"

# Implementation coverage. `report` is informational (safe mid-implementation); the gate
# itself lives in `check`, which is what CI must run once the surface is complete.
coverage:
	python3 $(SDK_DIR)/scripts/coverage_gate.py report

# Mid-implementation: reconciliation must pass; coverage gaps are reported but not fatal.
conformance: verify
	@python3 $(SDK_DIR)/scripts/coverage_gate.py report

# Final gate: no gap, no untested wiring, no stale methods. This is the CI target once
# the work in tasks/plan-schema-alignment-and-coverage.md is complete.
conformance-strict: verify
	@python3 $(SDK_DIR)/scripts/coverage_gate.py check --strict

build:
	cd $(SDK_DIR) && go build ./...

test:
	cd $(SDK_DIR) && go test ./...
