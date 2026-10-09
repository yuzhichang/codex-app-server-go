# Plan: align with the latest Codex app-server protocol and close capability gaps

| Item | Value |
|---|---|
| Date | 2026-10-08 (two review rounds incorporated) |
| Target repo | `github.com/Zealbase/codex-app-server-go` |
| Current baseline | SDK `HEAD=c319518` (2026-06-30); `VERSION=v0.1.2` (README says v0.2.0 — inconsistent) |
| Upstream reference | `/home/zhichyu/github.com/openai/codex` @ **`14c8b777`** |
| Upstream "version" | `codex-rs/Cargo.toml:163` `version = "0.0.0"` (**no meaningful version number**, see §2.1) |
| **Implementation scope** | **stable surface only** (= source `experimental=false`): experimental not implemented (R2); v1 deprecated and `attestation/generate` not implemented (R4) |
| Compatibility policy | **Align strictly with upstream, keep no old names / compatibility branches** (R3) |
| **Scope model** | `declared_stable` = **source non-experimental**; the export is used for **reconciliation** (R10) |

---

## 0. Decision Log

| ID | Decision | Impact |
|---|---|---|
| **R2** | No experimental entries are implemented | 89 entries (65 ClientRequest + 1 ServerRequest + 23 notifications) are **out of scope** (not stable) |
| **R3** | Anything that diverges from upstream is migrated, with no compatibility for old names | ① `item/mcp/requestApproval` → `mcpServer/elicitation/request`, old name deleted ② delete the `jsonrpc:"2.0"` field, no toggle ③ fix D2/D4/D5/D6/D7 in full |
| **T1.4** | Approved | Delete the `jsonrpc` field (tightened per R3; the `WithJSONRPCVersionField` toggle is cancelled); add `trace` (W3C Trace Context) |
| **T2.6** | Event delivery: no silent drop, bounded close, observable; timeout defaults to 5s | See §5.1 T2.6 (`publish` never blocks; waiting happens on the subscriber side; the terminal slot genuinely reserves capacity via `len(out)` per A8) |
| **R4** | The 5 v1 deprecated methods + `attestation/generate` are not implemented | Added to the whitelist (§5.7); inbound server-initiated requests are silently declined per §5.8 |
| **R9** | Unimplemented / unconfigured server-initiated request: silently decline + record locally, do not terminate the session | See §5.8 T2.13 |
| **R10** | **`declared_stable` is defined by the source's `#[experimental]` classification; the measured export is used only for reconciliation** | Corrects the previous version's error of "treating the export as the scope authority" (the export **excludes** methods and does **not filter** notifications) |
| **R11** | The set difference between source and export must equal the **explicitly registered set** (5 entries), compared as a **set** (not by count) | See T0.4 |
| **R12** | Terminal-slot reservation is implemented by constraining ordinary events to occupy ≤ `outCap-1` via `len(out)` | See T2.6 |
| **R13** | Terminal broadcast uses a **one-shot `stop` channel**; `Close()` only sets terminal + `close(stop)`, and `close(out)`/`close(done)` are performed only by the forwarding goroutine | See T2.6 (fixes A9; guarantees an idle subscriber's close always exits) |
| **R14** | The registered `kind` covers all four faces (including `client_notification_sender`), and face↔kind is surjective | See T1.1 (fixes A10; otherwise `initialized` cannot be registered) |

> ✅ **remote control is confirmed abandoned**: all 7 `remoteControl/*` methods are `#[experimental]` (`common.rs:1155-1193`), so per R2 they are out of scope; no exception.

### 0.1 First review round revisions (A1–A5)

| # | Finding | Revision |
|---|---|---|
| **A1** | Coverage-gate false positive: coverage judged by the generated `Method*` constants → the gap drops to zero the moment they are generated | T1.1 splits "declared set / implemented set"; the gate checks only the implemented set + three anti-fraud checks |
| **A2** | A separate forwarding goroutine still does not remove blocking on the shared path | T2.6: `publish` never blocks; waiting moves to the subscriber's execution path |
| **A3** | The terminal event had no viable delivery mechanism | T2.6: out-of-band `Err()`/`Done()` guaranteed + in-band reserved slot |
| **A4** | The premise "the stable export is a pure stable set" does not hold (notifications are not filtered) | §2.5 rewritten; notification scope is now decided by source classification |
| **A5** | Per-phase acceptance conflicted with the final DoD | Unified as **full delivery**; milestones are only a delivery order |

### 0.2 Second review round revisions (A6–A8)

| # | Finding | Revision |
|---|---|---|
| **A6** | The T0.4 assertion **fails on the current baseline**: source ClientRequest 173 / notifications 86 vs export 170 / 84 differ by 3 / 2, and **the source numbers are not a counting error** | **T0.4 redesigned**: register the 5 "export exclusions"; the assertion becomes **set equality** (an explicit difference set) instead of equal counts. **Withdraw the earlier "manually enumerated off by +3/+2" conclusion** (R11) |
| **A7** | Gap 59 double-subtracted legacy: the 105 stable-export entries already exclude the 3 legacy requests | **Revised to a set-difference computation** (R10): gap = **62**; a new task verifies the ownership of the 43 implemented methods (T1.1b) |
| **A8** | The forwarding pseudocode did not implement the terminal-slot reservation: `select { case out <- head }` fills all 128 slots, so the terminal event still cannot be guaranteed to write | **T2.6 corrected** (R12): constrain ordinary events to ≤ `outCap-1` via `len(out)`, and spell out the wait for consumer progress once capacity is exhausted; add a test for "consume nothing at all + deliver ≥ outCap events, then terminate" |
| **A9** | An idle subscriber's `Close()` may never exit: with an empty backlog it blocks on `waitFor(notify)` with nobody to wake it; and `done` is closed by that goroutine → `C()`/`Done()` never close | **T2.6 corrected** (R13): add a one-shot `stop` broadcast channel, **both wait points must select on `stop`**; state that `Close()` only sets terminal + `close(stop)`, and `close(out)`/`close(done)` are performed only by the forwarding goroutine; add a "subscribe and publish nothing, then Close" test |
| **A10** | The implementation registry missed ClientNotification: `declared_stable` contains `initialized`, but `kind` had no send path → it cannot be registered legally and the gate can never reach zero | **T1.1 corrected** (R14): add **`client_notification_sender`** to `kind`, and assert face↔kind is **surjective**; wiring evidence = a `transport.Notify(ctx, <Method*>, …)` call site; test evidence = `TestInitializeUsesProtocolMethods` |

> Neither review round modified files; the plan's author carried them out.

### 0.3 Implementation-time revisions (I1–I3, found while implementing M0 on 2026-10-08)

| # | Finding (all with measured evidence) | Revision |
|---|---|---|
| **I1** | **The anchor must be a codex repo commit, not the installed CLI.** Measured on this machine, `codex-cli 0.160.0` lags `14c8b777`: one fewer ClientRequest (`thread/attachmentOwner/list`, 104 vs 105) and one fewer ServerNotification (`thread/prediction/updated`, 83 vs 84). Implementing the original T0.1 ("run the CLI to generate") would **silently drop a method** | T0.1's sync input changes to **`--codex-src <repo>` (the main path)**; the CLI is used only for the new **`diff-cli` drift guard** (set comparison; fail on any mismatch) |
| **I2** | **The generated `version.go` must not contain a timestamp.** The original T0.2 required recording `GeneratedAt`, but a timestamp makes every `sync` produce a diff, **directly violating T0.1/T0.4's "idempotent, empty `git diff`" acceptance** | T0.2 drops `GeneratedAt`; `version.go` contains only deterministic values (commit, schema sha256, per-face counts). `sync` idempotency is confirmed by measurement |
| **I3** | Three implementation details clarified: ① the `schema/json/` committed in the repo *is* the **stable** surface (`ClientRequest.json` = 105, matching precomputed stable), so it can be the vendor source for the aggregate schema (622KB); ② the two internal-only notifications' exclusion reason **has an explicit upstream comment** (`common.rs:1977`/`:1979` *"This event is internal-only"*), upgrading the reason from inference to evidence; ③ precomputed decompresses to ~**4.9MB**, not worth vendoring | T0.1 vendors the **aggregate stable schema (622KB) + small derived method-set files**; `verify` therefore needs no `zstandard`, no repo, no network (CI-safe) |

| **I4** | **D4's original task description was wrong**: `emittedAtMs` is not a member of `params`. Upstream's `#[serde(flatten)]` makes the wire shape `{"method":…,"params":{…},"emittedAtMs":123}`, where the field is **sibling** to `method`/`params`, while the existing transport only reads `raw["params"]` and discards sibling fields at the read stage | D4 changed to **capture in the transport layer** (add `EmittedAtMs` to `Notification`), and the D4 row of §2.3 and T1.3 are corrected accordingly |
| **I5** | **The T1.1b audit found far more than the plan expected: the SDK has 5 (not 1) methods removed/renamed upstream**, all found mechanically by `scripts/coverage_gate.py`'s "wires-up-but-not-upstream" check (see `gen/unknown-methods.txt`):<br>`config/update` (SetModel/SetApprovalPolicy/SetSandbox use it) → upstream is now `config/value/write` (with `expectedVersion` optimistic concurrency)<br>`thread/rollback` → upstream is `thread/revert`<br>`turn/diff` → upstream has **no corresponding request method** (only the `turn/diff/updated` notification), so an alternative is needed or it must be deleted<br>`item/mcp/requestApproval` → `mcpServer/elicitation/request` (= the already-known D3)<br>`item/updated` → upstream **no longer has this notification** | Migrate/delete all per R3; add task **T1.5**. Note that `config/update`, `thread/rollback` and `turn/diff` are **breaking changes the plan had not previously identified** |
| **I6** | **The plan's "43 implemented" overstated completion**: it counted methods that merely "have wiring", while T1.1's criterion requires "wiring **and** test evidence". The mechanical count (`make coverage`) shows the real distribution: **59 implemented + 44 wired-but-untested + 72 not started + 5 to migrate** (declared_stable=182, whitelist 7) | Re-read the M2–M5 workload as "72 not started + 44 need tests"; adding tests is cheap and should be cleared first |

| **I7** | **The gate is "method-level" and cannot see "type-level" drift.** A full audit (all 118 `Method*` constants, inline/dynamic method names, `Call` sites) confirms the SDK's real RPC surface = **118 methods, of which exactly 3 do not exist upstream** (= `gen/unknown-methods.txt`), and there are **no additional phantom wrappers**. But comparing against the 660 `definitions` of `codex_app_server_protocol.v2.schemas.json` shows that of the 55 structs in `client_types_gen.go`, **27 have no same-named definition in the schema**. The causes fall into three classes: **(a) naming-convention differences (benign)** — upstream uses `*Params`/`*Response`, the SDK uses `*Request`/`*Result` (e.g. `InitializeParams`↔`InitializeParams`, `ThreadStartParams`↔`ThreadStartParams`); **(b) SDK-invented types** — `RPCRequest`/`RPCResponse`/`RPCNotification`/`RPCError` **do not exist at all** in the aggregate schema (upstream does not define a JSON-RPC envelope), and `InitializeCapabilities` corresponds to upstream's `InitializeCapabilities`; **(c) a real orphan** — `ThreadRollbackRequest` has no counterpart (upstream is `ThreadRevertParams`), which cross-confirms the method-level audit that `thread/rollback` is indeed gone. Also: `initialize`'s `InitializeCapabilities` naming is inconsistent with upstream, meaning a T1.2 codegen would emit a **different name**, so a naming map must be fixed first | T1.2 gains "type-level reconciliation": even without full codegen, compare SDK struct names against schema definition names and keep an explicit whitelist for **intentional renames**, or type-level drift stays invisible forever |
| **I8** | **`TurnRead` does not use a phantom RPC, but it uses a path upstream has deprecated**: `readTurn` (`wait.go:121-141`) goes through `thread/read{includeTurns:true}`. Upstream explicitly marks full hydrate as deprecated for paginated threads and recommends `thread/turns/list` + `thread/items/list` (`thread.rs:1703-1706`) | Do not change the method's ownership (`thread/read` really exists); record it as a "deprecated usage" and fold it into M5's read-path rework |

| **I9** | **`TokenUsage` is not merely a naming issue — the whole usage model is outdated**: upstream's `TurnCompletedNotification` has only `{threadId, turn}` (**no `usage`**) and the `Turn` struct has no usage field; usage is now carried by the separate `thread/tokenUsage/updated` notification, typed `ThreadTokenUsage { last: TokenUsageBreakdown, total: TokenUsageBreakdown, modelContextWindow? }`, where `TokenUsageBreakdown` has `{cachedInputTokens, cacheWriteInputTokens, inputTokens, outputTokens, reasoningOutputTokens, totalTokens}` (note **reasoningOutputTokens** — the SDK currently calls it `reasoningTokens` — and it is missing the 2 cached fields). So the SDK's `TurnCompletedEvent.Usage` / `TurnResult.Usage` model **something upstream does not send** | **A rename cannot fix this.** Need: ① add `ThreadTokenUsage`/`TokenUsageBreakdown` (upstream names and fields); ② change the usage source to `thread/tokenUsage/updated`; ③ remove `Usage` from `TurnCompletedEvent`/`TurnResult` (breaking). Recorded as a todo, not implemented |
| **I10** | **Type-level cleanup is done (27 → 2 drift)**: 16 pure naming differences were renamed to the upstream names (`*Request`/`*Result` → `*Params`/`*Response`, `Capabilities` → `InitializeCapabilities`); `SchemaItem`/`SchemaTurn`/`SchemaThread` → upstream's `ThreadItem`/`Turn`/`Thread`; 5 SDK-invented types were deleted (`RPCRequest`/`RPCResponse`/`RPCNotification`/`RPCError`/`InitializedNotification` — upstream does not define a JSON-RPC envelope at all; the wire envelope belongs to `internal/transport`) together with their `rpc_ext.go` and `envelope.go` aliases (`Request`/`Response`/`Notification`/`ErrorObject`, all unused); the real orphan `ThreadRollbackRequest` was deleted and `thread/rollback` was migrated to the **really existing** `thread/revert` (`ThreadRevertParams{threadId, beforeTurnId}` + `ThreadRevertResponse{thread, turnsBackwardsCursor, itemsBackwardsCursor}`) | Add `scripts/type_check.py` + `gen/type-allowlist.json`, wired into `make typecheck` / `conformance` / `conformance-strict`: **any type drift not on the whitelist fails the gate**, preventing re-decay. The 2 remaining drifts: `InitializeResponse` (correct name; only the v2 aggregate omits it → whitelisted with an explanation) and `TokenUsage` (see I9; deliberately left **not whitelisted** to keep alarming) |
| **I11** | **The whitelist narrowed from 8 to 5 entries (implementation-time correction).** The plan listed 8 (R4 v1 ×3 + server-initiated ×3 + internal-only notifications ×2), but implementation turned 3 of them into **real wiring**: `applyPatchApproval`/`execCommandApproval` are protocol-validly declined by `interaction.go` per §5.8 (exactly the behaviour §5.8 requires), and `rawResponseItem/completed` already had a typed decoder (`events_extra.go`). The gate's "a whitelist entry must not be contradicted by wiring" check forbids both, so those 3 leave the whitelist and are registered as implemented. Actual whitelist (`gen/whitelist.json`) = `getAuthStatus`/`getConversationSummary`/`gitDiffToRemote` (v1 deprecated) + `attestation/generate` + `rawResponse/completed` | The "8 entries" in §5.7 / §10.3 / §10.4 / §10.8 / Appendix A becomes **5**; add a **generated** read-only mirror `gen/not-implemented.txt` (derived from `gen/whitelist.json`; `make conformance-strict` checks its freshness) |

> Side conclusion 1: M0's mechanical extraction (`scripts/codex_schema_surface.py`) **independently reproduced every count in the plan** (173/65/108, 11/1/10, 86/23/63, 1/0/1), and all six set assertions pass — numbers previously derived by hand are now **machine-verified**.
>
> Side conclusion 2: the D1 removal was verified on this machine against a real `codex app-server --listen ws://…` (a handshake with no `jsonrpc` field and read-only RPCs both succeed). It also found that the **stdio path cannot be aligned**: it is driven by `jrpc2`, which itself emits and requires the `jsonrpc` field, so the `versionFixerReader` inbound patch must stay — that is "library behaviour", not a compatibility shim we can delete.

---

## 1. Summary (TL;DR)

1. **There is no "schema version" to align to.** The only anchor = a codex commit (**`14c8b777`**) + artifact sha256.
2. **The scope authority is the source's `#[experimental]` classification, not the export mode** (R10). The export **excludes** 5 methods and does **not filter notifications by experimental**.
3. **`declared_stable`**: ClientRequest **108**, ServerRequest **10**, ServerNotification **63**, ClientNotification **1**.
4. **Gaps to implement this round** (set difference, see §2.2): ClientRequest **62**, ServerRequest **2**, notifications reach the **61** target.
5. **Non-implementation baseline (declared stable but not implemented this round) = 5** (3 v1 deprecated + `attestation/generate` + 1 internal-only notification; narrowed from 8, see §0.3 I11); the 89 experimental entries are **out of scope because they are not stable**, and the two must be registered separately.
6. **`fs/*` (9), `plugin/*` (12) + `marketplace/*` (3), `mcpServer/*` (5) are all at 0**, and are the main things to fill in.
7. **reconnect has no protocol-level support upstream**, so a session supervisor must be built on the SDK side (§5.1).

---

## 2. Ground Truth

### 2.1 Upstream has no protocol version number

- `codex-rs/Cargo.toml:163` → `version = "0.0.0"` (`codex-app-server-protocol` inherits the workspace).
- `codex-rs/app-server-protocol/src/rpc.rs:11` → `JSONRPC_VERSION = "2.0"`, **referenced nowhere in the repo (dead code)**.
- Conclusion: **anchor = git commit + vendored artifact sha256**.

### 2.2 Scope and gaps (set model, measured at `14c8b777`)

| Face | Source total | Source experimental | **declared_stable** (source non-exp) | Export stable | Export experimental | Export exclusions | Whitelist | Implemented | **To implement** |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| ClientRequest | **173** | 65 | **108** | 105 | 170 | **3** | 3 (R4) | 43 | **62** |
| ServerRequest | **11** | 1 | **10** | 10 | 11 | 0 | 3 (R4) | 5 | **2** |
| ServerNotification | **86** | 23 | **63** | 84 | 84 | **2** | 2 (internal) | awaiting re-baseline | reach the **61** target |
| ClientNotification | **1** | 0 | **1** | 1 | 1 | 0 | 0 | 1 | 0 |

**Set relations (all must hold; asserted by T0.4)**:

1. `source_all − export_experimental == export-exclusion set` (**exactly** 5, registered by name, see below)
2. `export_experimental − source_all == ∅`
3. `export_experimental − export_stable == source experimental set` (ClientRequest 65 ✅, ServerRequest 1 ✅)
4. `export_stable(ServerNotification) == export_experimental(ServerNotification)` (**holds and is known upstream behaviour**: notifications are not filtered)
5. `export_stable(ClientRequest) == declared_stable(ClientRequest) − 3 legacy`
6. `export_stable(ServerNotification) == declared_stable(63) + 23 (leaked) − 2 (internal) == 84` ✅

**Export exclusion set (5 entries, R11 registration)**:

| Method | Face | Exclusion reason |
|---|---|---|
| `getAuthStatus` | ClientRequest | v1 deprecated top-level method; not emitted by the export |
| `getConversationSummary` | ClientRequest | same |
| `gitDiffToRemote` | ClientRequest | same |
| `rawResponse/completed` | ServerNotification | internal-only; not exposed to clients |
| `rawResponseItem/completed` | ServerNotification | same |

**Gap derivation (set difference, no double subtraction)**:

- ClientRequest: `declared_stable(108) − whitelist(3) − implemented(43) = 62`
  - Note: the 3 legacy methods are **neither in the export nor (per R4) in the whitelist already applied above**; do not subtract 3 again from "export stable 105" (that was the source of the previous version's 59).
- ServerRequest: `10 − 3 − 5 = 2`
- ServerNotification: `declared_stable(63) − internal-only(2) = 61` (implementation target)

> Method and evidence:
> - **Source counts**: the number of variants in `common.rs`'s four macro invocations → ClientRequest **173**, ServerRequest **11** (9 explicit wire + 2 derived from variant names as camelCase: `applyPatchApproval`/`execCommandApproval`), ServerNotification **86**, ClientNotification 1.
> - **Export counts**: decompress `schema/precomputed/app-server-exports-{stable,experimental}.json.zst` and take the union of `json_schema[*]["method"].enum`.
> - **Both datasets were independently re-checked**; the difference set is exactly the 5 above (they are absent from both exports).
> - ⚠️ Do not count the aggregate JSON with `grep '"method"'` (nested objects inflate it: measured 210/20/170).

### 2.3 Confirmed incompatibilities / defects (all must be fixed under R3)

| # | Problem | Evidence | Disposition |
|---|---|---|---|
| D1 | The SDK sends `"jsonrpc":"2.0"`; upstream neither sends nor expects it | upstream `rpc.rs:1-2`; SDK `internal/transport/transport.go` | **Delete, no toggle** |
| D2 | `InitializeCapabilities.OptOutNotificationMethods` is typed `bool`; upstream is `Option<Vec<String>>` | upstream `protocol/v1.rs:65`; SDK `client_types_gen.go` | Change to `[]string` (silent no-op bug) |
| D3 | The SDK uses `item/mcp/requestApproval`; upstream no longer has that name | SDK `envelope.go:46`, `interaction.go:218`; the upstream ServerRequest face | Migrate to `mcpServer/elicitation/request`, **delete the old name** |
| D4 | The notification envelope gained `ServerNotificationEnvelope.emittedAtMs`, unmodelled by the SDK | upstream `common.rs:2067-2080` | **Model it and capture it in the transport layer** (`emittedAtMs` is **sibling** to `method`/`params`, see T1.3's D4 note; putting it in the decoder does not work) |
| D5 | `InitializeCapabilities` lacks `explicitGatewayOauth`/`requestAttestation`/`extensions`; `ClientInfo` lacks `title`; `client.go` hardcodes `codex-go-sdk/0.1.0` | upstream `protocol/v1.rs:29-70`; SDK `client.go:90-109` | Add the fields; make `ClientInfo` configurable |
| D5b | It announces `experimentalApi: true` by default, contradicting R2 | SDK `client.go:95-97` | **Default to false** |
| D6 | Contradictory version metadata (`0.141.0` vs `0.142.0`; `v0.1.2` vs `v0.2.0`; the sync-script path is dead and it does not write a pinned version) | `version.go:3-4`, `VERSION`, `README.md:9,40,106`, `scripts/update-codex-go-schema.sh:6-7` | Produce from the generator / a single source |
| D7 | `llms.txt` writes the module path as `github.com/nharness/sdk/codex-go` | `llms.txt:2-5` | Correct it |

### 2.4 Upstream semantics of reconnect

- **No protocol-level ping/keepalive/session-resume.** The only ping is WS frame-level (`app-server-transport/src/transport/websocket.rs:363-373`).
- **Subscriptions are per connection**: `thread_state.rs:344-348`; disconnect does `remove_connection()` (`thread_processor.rs:3605`).
- **No client event replay**: `app-server/src/transport.rs:204-243`.
- **The official replacement = `thread/resume`** (`thread_state.rs:59-64`).
- The transport surface is only `stdio://`/`unix://`/`ws://`/`off` (`mod.rs:80-166`); **no HTTP/SSE**.

### 2.5 The relationship between export and source (revised A4/A6)

- CLI: `codex app-server generate-json-schema --out <DIR> [--experimental]` (`cli/src/main.rs:727-735`, dispatch `1421-1425`).
- **Conclusion 1**: `--experimental` **does** affect ClientRequest/ServerRequest (export exp-only = 65 / 1, **exactly matching** the source experimental counts).
- **Conclusion 2**: `--experimental` does **not** affect **ServerNotification** — stable and experimental are **both 84** (set-equal); all 23 source experimental notifications **leak** into the stable export.
- **Conclusion 3**: the export **excludes** 5 source methods (3 v1 deprecated client + 2 internal-only notifications).
- **Conclusion 4**: the stable export is a strict subset of the experimental export (stable-only per face = 0).
- **Inference**: scope must be defined by **source non-experimental** (R10); the export is for **reconciliation**, and reconciliation must be **set comparison + explicit difference-set registration** (R11).

### 2.6 Schema generation and distribution

- The runtime does not derive the schema: `precomputed_exports.rs:15-18` decompresses the embedded `.zst`.
- fixtures: `schema_fixtures.rs:95-153`.
- Upstream already commits vendorable artifacts: `schema/json/{codex_app_server_protocol.schemas.json, codex_app_server_protocol.v2.schemas.json, ClientRequest.json, ServerRequest.json, ServerNotification.json, ClientNotification.json}` + `schema/json/v2/*.json`.
- **Measured clarification (I3)**: the committed `schema/json/` **is** the stable surface — the method-set size of `schema/json/ClientRequest.json` = 105, matching precomputed stable. So `schema/json/codex_app_server_protocol.v2.schemas.json` (622KB) can be the vendor source for the stable aggregate schema.
- **Measured clarification (I3)**: precomputed `.zst` decompresses to ~**4.9MB** (typescript + json_schema + internal), **not worth vendoring**; instead vendor the aggregate schema + **small derived method-set files** (`.codex-schema/exports/{stable,experimental}-methods.json`), so `verify` needs no `zstandard`.

---

## 3. WS0 — schema sync infrastructure (highest priority)

### T0.1 Rewrite the sync script

- Replaces `scripts/update-codex-go-schema.sh` (dead path, D6).
- ✅ **Implemented**: `scripts/codex_schema_surface.py` (subcommands `sync` / `verify` / `diff-cli`), driven by `make sync`.
- **The input is a codex repo (not the CLI, see I1)**: `--codex-src <path>`; it takes from the repo
  1. the source `.../protocol/common.rs` → `declared_stable` + experimental annotations;
  2. `schema/precomputed/*.zst` → stable/experimental export method sets (for reconciliation);
  3. `schema/json/codex_app_server_protocol.v2.schemas.json` (stable aggregate, 622KB) → vendored to `internal/protocol/schema/`.
- Records `git -C <repo> rev-parse HEAD`; computes sha256 for every artifact; **writes no timestamp** (see I2).
- Adds `diff-cli`: set-compares a CLI-generated bundle against the anchor and fails on any mismatch (guards against "regenerating from a CLI that is behind/ahead").

### T0.2 `version.go` is written by the generator

- ✅ **Implemented**: `version.go` is generated by `sync` and `gofmt`-ed; it contains only **deterministic** values — `SourceCodexCommit`, `SchemaRevision` (sha256 of the aggregate schema), `SchemaTitle`, the four `DeclaredStable*`, the four `SourceExperimental*`, `NotInScopeTotal`.
- **No `GeneratedAt`** (I2: a timestamp breaks idempotency); **`PinnedCodexVersion` and `SchemaDefinitionCount` are deleted** (the latter came from the retired core-subset file).

### T0.3 Retire the "core subset" model

✅ **Implemented**: delete `internal/protocol/schema/v2.schema.json` (a 56-`$defs` cut-down) and its hashing logic; vendor the upstream artifact instead (`internal/protocol/schema/codex_app_server_protocol.v2.schemas.json`, 622KB stable aggregate).
Also retire the superseded scripts: `scripts/update-codex-go-schema.sh` (dead path D6), `scripts/generate_schema.go` and `scripts/compare_schema.py` (the old `make generate` / `make check-schema-drift` went through the CLI, with I1's version-skew risk). `Makefile` targets become `sync` / `verify` / `diff-cli` / `conformance`.

### T0.4 Source ↔ export reconciliation (**redesigned per A6**)

> ⚠️ **Original design flaw**: it asserted "source size == export size", but the baseline already differs by 3 / 2, so the assertion must fail; and **the source numbers are themselves correct** (not a counting error). It must become **set comparison + explicit difference-set registration**.

**Produces `gen/method-surface.json`** (the single machine-readable scope source):
```json
{ "method": "fs/readFile",
  "face": "client_request",
  "experimental": false,
  "experimental_reason": null,
  "in_export_stable": true,
  "in_export_experimental": true,
  "export_excluded": false }
```

**Produces `gen/export-exclusions.json`** (5 entries, human-reviewed + reasoned):
```json
[ { "method": "getAuthStatus", "face": "client_request", "reason": "v1 deprecated; not emitted by export" },
  { "method": "getConversationSummary", "face": "client_request", "reason": "v1 deprecated" },
  { "method": "gitDiffToRemote", "face": "client_request", "reason": "v1 deprecated" },
  { "method": "rawResponse/completed", "face": "server_notification", "reason": "internal-only" },
  { "method": "rawResponseItem/completed", "face": "server_notification", "reason": "internal-only" } ]
```

**Assertions (all set comparisons; any failure fails CI and requires human review)**:

| # | Assertion | Expected at the current baseline |
|---|---|---|
| 1 | `source_all − export_experimental == export-exclusions.json` | exactly 5 |
| 2 | `export_experimental − source_all == ∅` | ∅ |
| 3 | `export_experimental − export_stable == source experimental set` (per face) | ClientRequest 65, ServerRequest 1 |
| 4 | `export_stable(ServerNotification) == export_experimental(ServerNotification)` | equal (**registered as known upstream behaviour**; if it becomes unequal → upstream started filtering notifications, **must review and update scope**) |
| 5 | `export_stable(ClientRequest) == declared_stable(ClientRequest) − 3` | 105 == 108 − 3 |
| 6 | `export-exclusions.json` content unchanged from last time | otherwise requires human confirmation |

**Acceptance**: the script is idempotent (`git diff` empty); all six assertions pass; the manifest records the commit and sha256.

---

## 4. WS1 — align the JSON Schema with the latest codex code

### T1.1 Coverage determination and diff tooling (**redesigned per A1**)

> ⚠️ **Original design flaw**: it judged "implemented" by whether a `protocol.Method*` constant exists. But T1.2 **generates all the constants** from the schema, so the gap drops to zero the moment they are generated — even with no `Client` method, dispatcher branch or notification decoder. **"Protocol declaration" and "actual implementation" must be registered separately, and the gate must check only the latter.**

- **Two sets**:
  - **declared**: the constants and types generated by T1.2. **It only means the protocol has it; it is no coverage signal at all.** The generated artifacts must carry a prominent header comment.
  - **implemented**: explicitly registered in `gen/implemented-methods.json` (human-reviewed, updated with each PR):
    ```json
    { "method": "fs/readFile", "face": "client_request", "kind": "client_method",
      "call_site": "(*Client).FSReadFile", "test": "TestFSReadFile" }
    ```
    `kind` ∈ `client_method` | `server_request_handler` | `notification_decoder` | `client_notification_sender`.
- **face ↔ kind must cover one-to-one (fixes A10)**: `client_request` → `client_method`; `server_request` → `server_request_handler`; `server_notification` → `notification_decoder`; `client_notification` → **`client_notification_sender`** (the **send** path, client → server, not the decode path). The four form a surjection; the checker must assert that "every face has a kind usable for registration", otherwise `declared_stable`'s `initialized` (ClientNotification) can never be registered legally and the gate can never reach zero.
- **Gate**: `declared_stable − whitelist(§5.7, 5 entries) − implemented == ∅`.
- **Three anti-fraud checks (all must hold, otherwise the entry counts as not implemented)**:
  1. **Wiring evidence (dispatched by kind)**:
     - `client_method` → the `Method*` call site appears inside some `Client`/`SessionThread` method body (**excluding** the constant-definition file and generated artifacts);
     - `server_request_handler` → `Dispatcher.HandleServerRequest` has a matching `case`;
     - `notification_decoder` → `decodeEvent` / `extraEventTarget` has a matching `case`;
     - `client_notification_sender` → a **`transport.Notify(ctx, <Method*>, …)`** call site exists (**excluding** the constant-definition file and generated artifacts). Current baseline: `initialized`'s evidence is `Notify(ctx, protocol.MethodInitialized, nil)` inside `(*Client).Initialize`.
  2. **Test evidence**: a named test exists and actually references the method constant or exercises the branch. Current baseline: `initialized` is covered by `client_test.go:62` `TestInitializeUsesProtocolMethods`, which asserts both `callMethod == "initialize"` **and** `notifyMethod == "initialized"` (`client_test.go:73-78`), usable directly as `client_notification_sender` test evidence; `client_extended_test.go:31` `TestInitializeNotifyFails` covers the Notify failure path.
  3. **Two-way agreement**: no orphan registration, and no unregistered wiring.
- **T1.1b Verify the ownership of the 43 implemented methods (new per A7)**: audit each of the 43 currently implemented methods and classify as
  (a) within `declared_stable` and still valid → counts as implemented;
  (b) removed/renamed upstream (e.g. `item/mcp/requestApproval`) → delete per R3;
  (c) experimental → remove per R2, or keep with an explicit reason.
  Write the result to `gen/implemented-methods.json` with a reconciliation note. **Until (b) is cleaned up, 43 must not be treated as a valid base.**
- ✅ **Implemented**: `scripts/coverage_gate.py` (`report` / `write` / `check [--strict]`) + `gen/whitelist.json` (7 entries) + `gen/implemented-methods.json` + `gen/unknown-methods.txt`; `make coverage` / `make conformance` / `make conformance-strict`.
  - **The criterion binds kind to face** (rather than "scan every role and take a priority" — `client.go` carries both client_request and the single client notification, and that approach mis-attributed requests to `client_notification_sender` and then dropped them in the Notify check, empirically producing 44 phantom gaps).
  - **Test evidence is relaxed** to "references the constant **or** the wire method string": the SDK's tests generally drive mock servers with wire strings, and requiring constants would badly undercount.
  - Four self-checks added: a whitelist entry must be in `declared_stable`; a whitelist entry must not already be implemented (contradiction); methods wired but removed upstream must be empty; two-way agreement.
- **Measured result (`make coverage`)**: `declared_stable=182 / whitelist=7 / implemented=60 / gap=115`, of which **71 not started + 44 wired but untested**; plus **2 methods to migrate** (`config/update`, `item/mcp/requestApproval`).
- **Outputs**: `gen/method-surface.json`, `gen/export-exclusions.json`, `gen/not-in-scope.txt`, `gen/whitelist.json`, `gen/implemented-methods.json`, `gen/unknown-methods.txt`.

### M2 implementation status (2026-10-08)

- ✅ **T1.2 partially landed**: add `scripts/gen_go_types.py` + `make generate-types`, generating Go types from the vendored aggregate schema (**only definitions the SDK is missing**, to avoid clashing with the 55 existing hand-written structs in `client_types_gen.go`; the full replacement is still T1.2's remaining work). Two schemars pitfalls were handled: a single-element `allOf`-wrapped `$ref` must be unwrapped (otherwise every documented path field degrades to `json.RawMessage`), and an `object` with no properties should become `struct{}` rather than `map[string]any`.
- ✅ **§5.2 fs is done (9 RPC + 1 notification)**: `fs.go` provides `FSReadFile`/`FSWriteFile`/`FSCreateDirectory`/`FSGetMetadata`/`FSReadDirectory`/`FSRemove`/`FSCopy`/`FSWatch`/`FSUnwatch`, plus `FSReadFileBytes`/`FSWriteFileBytes` for base64 round-trips. The `fs/changed` notification **already existed** (`events_extra.go` already had `FsChangedEvent` and a decode branch) — i.e. the notification surface predated the RPC surface.
- ✅ **§5.3 MCP is done (5 RPC)**: `mcp.go` provides `MCPServerOauthLogin`/`MCPServerStatusList`/`MCPServerResourceRead`/`MCPServerToolCall`/`ConfigMCPServerReload`; the two notifications (`mcpServer/oauthLogin/completed`, `mcpServer/startupStatus/updated`) likewise **already existed**.
- ✅ **MCP elicitation is implemented** (`f5f43e0`): `Dispatcher.Elicitation` is wired; `item/mcp/requestApproval` → `mcpServer/elicitation/request` is replaced; with no handler it replies **decline** (R9). The reason the types **must be hand-written** still holds: the `mcpServer/elicitation/request` method itself is **stable**, but its **payload types are not in the vendored schema at all** — because `McpServerElicitationRequestParams.request` carries `#[experimental(nested)]`, so the export omits it. So these types must be **hand-written from the Rust source** (a legitimate "invent only when genuinely necessary" case, to be registered with a reason in `gen/type-allowlist.json`), along with the `item/mcp/requestApproval` → `mcpServer/elicitation/request` dispatcher replacement (R3/D3).
- ⚠️ **The gate itself fixed a bug that under-reported**: `coverage_gate.py` used a **hardcoded file list** to determine client_method wiring evidence, so methods implemented in new files (`fs.go`/`mcp.go`) were **completely invisible** (80 entries under-reported). Changed to **locate by role**: client_method = any call site outside the decode/handle layers and generated artifacts; notification_decoder / server_request_handler stay confined to their respective layers. After the fix, implemented went **60 → 74**.

### M4 implementation status (2026-10-08)

- ✅ **§5.5 done (15 entries)**: `plugin.go` provides `MarketplaceAdd`/`MarketplaceRemove`/`MarketplaceUpgrade` + `PluginList`/`PluginInstalled`/`PluginReconcile`/`PluginRead`/`PluginSkillRead`/`PluginShareSave`/`PluginShareUpdateTargets`/`PluginShareList`/`PluginShareCheckout`/`PluginShareDelete`/`PluginInstall`/`PluginUninstall`. `plugin/search` is experimental and not implemented per R2. Upstream's `serialization: global("config")` semantics are registered in a code comment (server-side serialization; do not rely on the emission order of two config changes).
- ✅ **§5.6's app group done (3 + 1 notification)**: `app.go` provides `AppsList`/`AppsInstalled`/`AppsRead`; the `app/list/updated` notification **already existed**.
- ⚠️ **codegen fixed 3 more mapping defects** (all exposed by tests, all affecting the quality of M5's output):
  1. **Nullable refs** (`anyOf: [$ref, null]`) previously degraded to `json.RawMessage` — and this pattern is **widespread** in the schema (e.g. `pluginInstallParams.marketplacePath`). Now mapped to `*T`.
  2. **Initialism not split out of camelCase**: `remotePluginId` as a single token matched no initialism table, producing `RemotePluginId`, giving two spellings for the same concept next to the existing hand-written `RemotePluginID`. Changed to split by camelCase first.
  3. **Transitive dependencies bypassed dedup**: the `existing` skip logic was not applied to transitive-reference traversal, causing duplicate declarations (the compiler reported `AbsolutePathBuf`/`SkillSummary` redeclared).
- ✅ **`generate-types` is idempotent** (re-running leaves `git diff` empty).

### T1.2 wrap-up (full codegen) — **measured before starting: this is not dedup, it is fixing protocol violations**

Before starting, a **field-by-field comparison** was done (the 50 hand-written structs in `client_types_gen.go` vs the generator's output for the same names):

| | Count |
|---|---|
| Hand-written structs | 50 |
| Of which have a same-named definition in the aggregate schema | **49** (the sole exception `InitializeResponse`, whitelisted) |
| **With inconsistent field shape** | **26** (`make type-shape-check` reproduces) |

> ⚠️ **Self-correction (important)**: I initially asserted from this that "`SessionThread.Run` sends a malformed payload" — **that was wrong**. `internal/protocol/schema/marshal_ext.go` provides a **hand-written `MarshalJSON`** for `TurnStartParams` / `TurnSteerParams` / `ThreadStartParams`, whose `wireInput` **correctly converts** a Go string to `[{"type":"text","text":…}]` — **the wire shape was always right**. The design deliberately keeps the "use a string on the Go side" ergonomics. Lesson: a type/field name mismatch **does not equal** a protocol error; you must go one layer down to serialization behaviour.
>
> But going that extra layer **did dig up two real problems**:
> 1. **`TurnStartParams.Skill` is settable but never reaches the wire** — the hand-written marshaller uses an `alias` struct that **re-lists the fields one by one**, and it missed `Skill`; its test only asserted that `req.Skill` was set (**asserted the local field, not the payload**), so the no-op went unnoticed for a long time. And **upstream `TurnStartParams` has no `skill` field at all** ⇒ that field and the `WithSkill` option are **dead API** and were deleted (with their misleading test).
> 2. `Environments` is marked upstream as **`#[experimental("turn/start.environments")]`** ⇒ per R2 it should not be exposed (and upstream's type is `Vec<TurnEnvironmentParams>`, the SDK's is `[]string`). **Not changed**, recorded as a todo.
>
> **A new invariant test `TestMarshalledParamsSendEveryField` was added**: using reflection it fills **every settable field** of the three structs behind the hand-written marshallers with a non-zero marker, then asserts **every field appears in the wire JSON**. It was **verified to fail** (temporarily adding `Skill` back ⇒ reports `field "skill" is settable but never reaches the wire`), so it is not a no-op. This turns "adding a field must also update the second place" from an **implicit obligation** into a **checked property**.

### T1.2 triage conclusion (26 entries, each graded)

Triage method: classify by **JSON semantics rather than the Go type text** (`*T` and `T`, `string` and a string alias are the same family and not a difference), then cross two points — **whether the type is aliased/used**, and **whether a hand-written marshaller takes over the wire shape**.

**① Dead duplicates (2, safe to delete or freely regenerate)**
`schema.Thread` / `schema.Turn` have the **same names but are different objects** from the runtime types `protocol.Thread` / `protocol.Turn`: the latter are declared in `internal/protocol/types.go` with a **custom `UnmarshalJSON`** (lenient time parsing), and the root package's `codexgo.Thread`/`Turn` aliases point at `protocol.*`. **Nothing aliases or decodes `schema.Thread`/`schema.Turn`** ⇒ they are **dead copies**.
> This also explains why the two most alarming differences (`CreatedAt`/`StartedAt` hand-written as `*time.Time`, upstream `int64`) **are harmless** — what actually decodes is the runtime type with lenient parsing. **The two items that "look most dangerous" carry zero real risk.**

**② Aliased and used ⇒ the shape matters (real work)**
`ClientInfo`, `InitializeParams`, `InitializeCapabilities`, `ThreadStartParams`, `TurnStartParams`, `TurnSteerParams`, `ThreadListParams`, `ThreadForkParams`, `ThreadResumeParams`, `ReviewStartParams`, `ThreadGoal{Set,Clear}Params`, `Skills*Params`, `SkillMetadata`, `TurnError`.

**③ Not used outside the package (4, harmless differences)**
`SkillInterface`, `SkillSummary`, `ThreadForkResponse`, `ThreadListResponse`.

**④ Real problems (✅ all four fixed)**

| Type | Problem | Fix |
|---|---|---|
| `ReviewStartParams` | Missing the required `target`; `turnId` invented; the response is discarded | `dc46d71`: model `ReviewTarget` (a 4-variant flat union) + `ReviewDelivery` + 4 constructors; remove `turnId`; `ReviewStart` returns `ReviewStartResponse` |
| `ThreadResumeParams` | 4 of 5 fields invented (`history`/`path`/`initialTurnsPage`), `excludeTurns` mistyped (`[]string` vs `bool`); 11 upstream fields unreachable | `1b8f7c9`: align the field set; `ExcludeTurns bool`; also unify the duplicate `SandboxMode` with the schema type as an alias |
| `TurnError` | `code`/`data` invented; missing `codexErrorInfo`/`additionalDetails`/`misalignment` (**all lost on decode**) | `52c1086`: adopt the generated shape. Note the distinction: these are **output** fields, readable is enough; `CodexErrorInfo` is `*json.RawMessage`, `MisalignmentErrorDetails` is an internal struct pointer (readable, need not be constructible) |
| `GitInfo` | 5 of 6 fields invented; contradicts the **existing and correct** `ThreadMetadataGitInfo` (patch direction) | `ca91695`: align the read direction to `{branch,originUrl,sha}`; `ThreadMetadataGitInfo` **deliberately stays a separate declaration** — the read direction uses `string`, which cannot distinguish "absent" from "empty", while the patch direction must (nil leaves it alone, `""` clears it) |

`make type-shape-check`: **26 → 23** (3 fully cleared; `ThreadResumeParams` is still flagged, but only for **three deliberate type simplifications**, explained in a type comment).

**④ Original grading (kept to record the process)**

| Type | Problem | Nature |
|---|---|---|
| `ReviewStartParams` | Missing the **required** `Target` (upstream `ReviewTarget`) and no marshaller | **Confirmed genuinely broken**: upstream `required: [target, threadId]`, while the SDK sends only `{threadId, turnId}` ⇒ the server must reject it. Also: `Client.ReviewStart` **discards the response** (`nil`), whereas upstream does return a review result. **Why it was never found**: the only thing covering it is a **generated smoke test**, and the mock server returns success for any payload ⇒ **it cannot assert payload validity**. The fix needs: model a `ReviewTarget` tagged union + `ReviewDelivery`, and change the field set and callers. |
| `ThreadResumeParams.ExcludeTurns` | Hand-written `[]string`, upstream **`bool`** | **Type mismatch**, sending the wrong JSON shape |
| `TurnError` | Missing `CodexErrorInfo`/`AdditionalDetails`/`Misalignment`, extra `Code`/`Data` | **Fields lost on decode** + invented fields |
| `GitInfo` | `Root`/`Commit`/`Remote`/`Dirty`/`Detached` all invented; upstream is `originUrl`/`sha` | **Invented struct**, decodes to empty |

**⑤ Capability gaps (not errors, but the user cannot express them)**
Several request params lack upstream fields; the largest are `ThreadForkParams` (14), `ThreadListParams` (11), `ThreadResumeParams` (11), `ThreadStartParams` (10).

**⚠️ A methodological correction**: when I first counted "which types are used" I matched by **bare name**, conflating `schema.Thread` with `protocol.Thread` and `codexgo.Thread`, and got "22 used by production code" — that number is **untrustworthy**. Only after checking by **package-qualified name** did I get the triage above. **Same name / different type is a recurring trap in this codebase**; any name-based count must qualify the package.

**Next steps (not started)**: first fix ④'s four items (all **concrete, verifiable** bugs), then decide whether ②'s remaining items are generated, whitelisted, or field-completed; only then promote `type_shape_check` to a gate.

**The following is the original measurement (some conclusions were overturned by the corrections above; kept to record the process):**

| Type | SDK hand-written | Upstream actual |
|---|---|---|
| **`TurnStartParams.Input`** | `string` | **`Vec<UserInput>` (required)** |
| `TurnSteerParams.Input` | `string` | `Vec<UserInput>` |
| `ReviewStartParams` | `turnId` | `target` (**required**) + `delivery` |
| `GitInfo` | `Root`/`Commit`/`Remote`/`Dirty`/`Detached` (invented) | `originUrl`/`sha` |
| `SkillInterface.IconLarge` | `string` | `*AbsolutePathBuf` |
| `ClientInfo.Name`/`Version` | `omitempty` | required |

**`TurnStartParams` is the critical one**: `SessionThread.Run` (the SDK's **primary entry point**) builds `TurnStartParams{ThreadID: …, Input: input}` at `thread.go:125`, where `Input` is a Go string ⇒ it actually sends `{"input":"hello"}`, while upstream requires `{"input":[{"type":"text","text":"hello"}]}`.

**Why it went unnoticed** (three blind spots compounding):
1. The Go type system does not check the wire shape — nothing to find at compile time;
2. The mock-server tests **assert against the same hand-written type**, a **circular validation**;
3. Real-server tests are skipped without a codex binary.

Plus a methodological problem: `type_check.py` **compares type names only**. All names correct, all shapes wrong, the gate is green. That is exactly the layer I7 pointed at but did not close.

**A new `scripts/type_shape_check.py`** (`make type-shape-check`, folded into `make conformance`'s **report** step) compares hand-written vs generated field by field, making these 26 **enumerable and reproducible**.
- **Deliberately not in `conformance-strict` yet**: this is T1.2's **outstanding work** and it is not yet triaged (which to generate, which to whitelist); making it a gate would just yield a "known failure". Better to make the list solid first. It must be added to strict once the migration is done.

**Migration workload (not started)**: ① make `--from-surface`'s output **replace** `client_types_gen.go` rather than coexist; ② `UserInput` and other tagged unions currently degrade to `json.RawMessage` (`type UserInput = json.RawMessage`), and must be **modelled as structs with a discriminator field** before `Input` is usable (the same approach as elicitation); ③ fix the callers of `Run`/`TurnStart`/`ReviewStart` — **breaking public API changes** (`Run(ctx, string)`'s signature must be redesigned to accept structured input, or a `RunInputs(ctx, []UserInput)` provided); ④ remove the naming inconsistency between `Init` and `initialize` (`Capabilities` vs `InitializeCapabilities`); ⑤ for `GitInfo` et al., confirm whether upstream really has a replacement.

> ⚠️ Conclusion: **the real value of "eliminating the dual track" is not tidiness — it has already masked a protocol violation in the main entry point.** Fix `TurnStartParams` first (or at least make it fail loudly), before talking about type tidiness.

### Decision (user-ruled): **keep the generated types, discard the SDK's hand-invented ones**

The user ruled to adopt the generated shapes and discard the SDK's own `SandboxMode`/`ApprovalMode`. **Executing that decision surfaced two "value-level" bugs**, far more serious than type tidiness — they are not "ugly shapes" but **values the server does not accept**:

| Field | SDK sends | Upstream actually requires |
|---|---|---|
| `TurnStartParams.SandboxPolicy` | a bare string `"workspace-write"` | an **object** `{"type":"workspaceWrite"}` — the discriminators are **camelCase** (`readOnly`/`workspaceWrite`/`dangerFullAccess`/`externalSandbox`), **different** from `SandboxMode`'s spellings (`read-only`/`workspace-write`/`danger-full-access`) |
| `TurnStartParams.ApprovalPolicy` (via `ApprovalMode`) | `deny_all` / `auto_review` / `on-request` / `never` | upstream's `AskForApproval` enum has only **`untrusted`/`on-request`/`never`** ⇒ **`deny_all` exists in no enum**; **`auto_review` belongs to `ApprovalsReviewer`** (`user`/`auto_review`/`guardian_subagent`) and was **misattributed** |

So `sandboxPolicy` was **doubly wrong** (wrong JSON type + wrong value spelling), and `ApprovalMode` **merged two upstream enums into one**.

> ✅ `fadc033` **sandboxPolicy fixed**: `SandboxPolicy` is built as a 4-variant flat discriminated struct + constructors (consistent with `UserInput`/`ReviewTarget`/elicitation); `WithSandbox(string)` → **`WithSandboxPolicy(SandboxPolicy)`** (a bare string **cannot** be a legal policy, so it is deleted rather than reinterpreted; and it had no callers); `WithSandboxMode` becomes a **conversion rather than a type cast** (`SandboxPolicyFromMode`).
> ⚠️ The original test asserted `req.SandboxPolicy == "workspace-write"` — **it pinned the bug** rather than catching it. Now it asserts the discriminator value, plus a new payload assertion.
> ⚠️ **Verification methodology**: my first negative control changed `SandboxPolicyFromMode`, which that test **does not call** ⇒ the control "passed" and proved nothing. After changing the constructor the test actually calls, it reported `type = "workspace-write", want "workspaceWrite"` as expected.

> ✅ `51c2260` **`ApprovalMode` split**: `AskForApproval` (`untrusted`/`on-request`/`never` **or** `{"granular":{...}}`) and `ApprovalsReviewer` (`user`/`auto_review`/`guardian_subagent`) are built as **two** types; `ApprovalMode` and `WithApprovalMode`/`WithThreadApprovalMode` are deleted; `WithApprovalPolicy` now takes `AskForApproval` (the old `string` **could carry any value**).
> - **Implementation note**: one arm of `AskForApproval` is a **bare string** ⇒ it **cannot** use `SandboxPolicy`'s flat struct with a `type` field (a struct **always** encodes as an object), so it must have **custom `MarshalJSON`/`UnmarshalJSON`**. The `granular` object's field names are **snake_case** upstream (unlike the rest of the protocol), and its three required fields carry **no `omitempty`**.
> - The three request structs gain `ApprovalsReviewer` — that is where `auto_review` belongs.
> - Added `Ptr[T]` (optional pointer fields are common in literals).
> - Tests cover both branches and their round-trips; the payload test asserts that `approvalPolicy` is a bare string and `auto_review` lands in `approvalsReviewer`. **The negative control was actually run** (wrapping the enum branch as an object ⇒ reports `got {"policy":"never"}, want "never"`).

### Second-round shape triage: 16 "invented fields", each graded (`30ea584` found a 6th real bug)

Method: look at **each invented field** (present in the SDK, absent upstream) rather than at the struct. The 16 fields are spread across 9 structs.

| Verdict | Item | Note |
|---|---|---|
| ✅ **Real bug (fixed)** | `TurnSteerParams.TurnID` | Upstream `required: [expectedTurnId, input, threadId]`; the SDK sends `turnId` (which upstream **does not define at all**) and has **no `expectedTurnId` whatsoever** ⇒ **steering never worked**; and the field had **no `omitempty`**, so the wrong field **was sent every time**. Changed to `ExpectedTurnID` (wire `expectedTurnId`), added `clientUserMessageId`, and `TurnSteer` no longer discards the response |
| ⚪ **Harmless: the type is dead** | `ThreadListResponse.Threads`/`Cursor` | Looks like a decode bug, but `Client.ThreadList` **decodes into an anonymous `{data}` struct itself** (`client.go:348`) and never uses this type ⇒ the difference is unreachable. **Check reachability before changing**, to avoid fixing an unused type |
| 🔤 **Naming only** (same json tag, 4 items) | `InitializeCapabilities.MCPServerOpenAIFormElicitation`, `InitializeParams.InitializeCapabilities`, `SkillsListParams.CWDs`, `schema.Turn.DurationMS` | The generator produces `McpServerOpenaiFormElicitation`/`Capabilities`/`Cwds`/`DurationMs` with **identical json tags** ⇒ rename only, zero behavioural risk. **Not done** |
| ⚪ **Harmless: upstream ignores unknown fields** | `TurnStartParams.Permissions`/`CollaborationMode`/`MultiAgentMode`/`Environments`, `ThreadStartParams.RuntimeWorkspaceRoots`/`DynamicTools`/`Metadata`/`Environments`, `ThreadForkParams.TurnID` | They reach the wire, but upstream **does not error** on fields it does not know (no `deny_unknown_fields`) ⇒ dead weight, not errors. **Not done** (of these, `environments` is marked experimental upstream and should not be exposed per R2) |

> ⚠️ **Another "test pinned the bug"**: the existing `TestSessionThreadSteer` asserted `req.TurnID` — it was **written against the same wrong model**, so it passed. This is the same pattern as the `sandboxPolicy` test (`30ea584` corrected it too). **This round hit "the test copied the wrong model" three times**, worth recording as a pattern rather than a coincidence.

**Capability gaps (GAP) are still untriaged**: 73 missing fields in total, the largest being `ThreadForkParams`(14), `ThreadListParams`(11), `ThreadForkResponse`(10), `ThreadStartParams`(9), `Thread`(8), `TurnStartParams`(7). Their nature is "**the user cannot express it**", not "a wrong value is sent".

### T1.6 Generator support for discriminated unions (**feasibility verified: doable, and a prerequisite for the T1.2 migration**)

**Why do it**: the generator degrades every union to `= json.RawMessage`, which turns the T1.2 migration into a **regression** (next section). With this capability in place, ① the migration is safe; ② currently degraded types get typed access; ③ the hand-written copies I have **already written this pattern 4 times** (`UserInput`/`ReviewTarget`/`SandboxPolicy`/elicitation) can be deleted.

**Measurement (vendor aggregate schema)**:

| Union category | Count | Automatable? |
|---|---|---|
| **tagged-object** (every arm is an object with a `type`-enum discriminator field) | **36** | ✅ Yes → flat discriminated struct |
| **string-arm** (contains a bare string arm, e.g. `AskForApproval`) | **17** | ⚠️ Needs a **custom `MarshalJSON`** (a struct always encodes as an object and cannot express a bare string arm) |
| Other | 12 | Needs case-by-case judgement |

Of these, **28 are `= json.RawMessage` today** (13 tagged-object + 10 string-arm) — i.e. degradation is **widespread**, not a special case.

**Breakdown of the 36 tagged-object unions**:
- **27 have no field-name conflicts between arms** ⇒ a flat struct can be produced safely.
- **9 have conflicts** (same name, different type, e.g. `CommandAction.path`: `LegacyAppPathString` vs `null|string`; `ResponseItem.content`: `array` vs `array|null`).
- **34 nested union fields** (an arm field is itself a union).

**Implementable rules (verified sufficient to cover all 36)**:
1. Merge arm fields by name: a `Type string` discriminator field + the union of the arms' fields;
2. **Nested union fields → `json.RawMessage` fallback** (exactly what I did for the hand-written `SandboxPolicy.networkAccess`);
3. **Conflicting fields → `json.RawMessage` fallback**, rather than inventing merge semantics (keep the shape, do not guess);
4. Emit a `Type` constant block + per-arm constructors; order the fields by the generator's alphabetical convention.

> Rules 1–4 should produce a shape **equivalent** to my hand-written version for `SandboxPolicy` (its `networkAccess` happens to go through rule 2). `UserInput`/`ReviewTarget` have no conflicts and go through rule 1.

**Not yet implemented**: this is a generator capability extension, requiring all artifacts to be regenerated and compared item by item; it is separate work. **Until it lands, the T1.2 migration is still a regression and must not be done.**

### T1.2 remaining part (cleanup, not bug fixes) — measured, **not recommended to start directly**

**Measurement method**: point the generator at an **empty directory** (`existing={}` cleared) to get "what the whole surface can generate", then compare against the hand-written subset.

| | Count |
|---|---|
| `client_types_gen.go` hand-written types | 58 (794 lines) |
| The generator **can also produce** | **52** |
| Of which **byte-identical** to hand-written | **23** (pure duplicates; deleting changes zero behaviour) |
| Must stay hand-written | 6 (`AskForApprovalGranular`/`CommandExecutionApprovalDecision`/`FileChangeApprovalDecision`/`InitializeResponse`/`ItemKind`/`PermissionGrantScope`, all already on the name whitelist) |

> ⚠️ **It cannot be migrated wholesale — that would regress three real fixes.** The generator degrades **tagged unions to `json.RawMessage`**:
> `type SandboxPolicy = json.RawMessage`, `type AskForApproval = json.RawMessage`, `type ReviewTarget = json.RawMessage`.
> But the three hand-written versions are **discriminated structs**, exactly what `fadc033`/`51c2260`/`dc46d71` fixed (the `sandboxPolicy` object vs string, `ApprovalMode` merging two enums, `ReviewStartParams` missing the required `target`).
> **Make the generator emit discriminated unions first, then talk about migrating**; otherwise the migration is a regression.
>
> ⚠️ **Another reason the migration is not mechanical**: the generator's comment once claimed "skipped types are still traversed", **but the code does not traverse them** — traversal starts from `wanted`, which already excludes `existing`. Consequence: **a type reachable only through other hand-written types will not be regenerated after deletion**, and the failure is silent (the generator emits nothing; the compiler reports an undefined name). So the migration must be **done along the reference chain as a whole**, not type by type. The comment has been corrected.

**Conclusion**: this is **cleanup**, not correctness work, and it needs the generator's discriminated-union capability first. Not recommended to start without a clear goal.

### T1.5 Migrate methods upstream deleted/renamed (**added by I5**)

`scripts/coverage_gate.py`'s `wires-up-but-not-upstream` check mechanically found 5 items, 4 more than the plan expected. All are migrated per R3, **leaving no old name**:

| Current SDK method | Upstream status and the **authoritative param shape** (from the codex source) | Disposition |
|---|---|---|
| `config/update` | Does not exist. Replaced by `config/value/write`, whose `ConfigValueWriteParams { key_path, value, merge_strategy, file_path?: Option, expected_version?: Option }` (`protocol/v2/config.rs:1101-1110`); the version must first be read via `config/read` (`ConfigReadParams { include_layers, cwd? }` → `ConfigReadResponse { config, origins, layers? }`) from `origins[key].version` | Migrate `SetModel`/`SetApprovalPolicy`/`SetSandbox`. **Not a rename**: add `keyPath`+`mergeStrategy` and a new optimistic-concurrency read-version flow; also determine the real config key names for `model`/`approval_policy`/`sandbox_mode` |
| `thread/rollback` | Does not exist. Replaced by `thread/revert`, `ThreadRevertParams { thread_id, before_turn_id }` (`protocol/v2/thread.rs:1288-1292`), meaning "**exclude that turn and every later turn**"; response `ThreadRevertResponse { thread, cursor }`, `turns` is always empty and must be backfilled with `thread/turns/list` | **Not a rename → a semantic change**: the current `ThreadRollback(ctx, ThreadRollbackRequest{TurnIDs []string})` is "roll back by id list", which cannot map one-to-one. The API must be redesigned (a single `beforeTurnID` + cursor backfill), and `SessionThread.Rollback` updated too |
| `turn/diff` | **No corresponding request method** (`turn/*` has only interrupt/settings-update/start/steer; the `Turn` struct also has **no diff field**, `thread_data.rs:386-409`). Diff data is still obtainable (the aggregate diff is pushed only via the `turn/diff/updated` notification; per-file diffs are in `FileUpdateChange.diff`, `item.rs:1146-1150`), but at the **JSON-RPC level there is no `turn/diff` method** | ✅ **Done (user decision)**: JSON-RPC has no `turn/diff`, so **the SDK does not implement it**. Delete `MethodTurnDiff`, `TurnDiffRequest`/`TurnDiffResult`, `Client.TurnDiff`, `SessionThread.GitDiff` and the `types.go` aliases; also clean the dangling references in `docs/api-reference.md`, `docs/index.md`, `llms.txt`, `llms-full.txt`; delete `TestSessionThreadGitDiff`. **Keep** `TurnDiffUpdatedEvent` (it is a real upstream notification) |
| `item/mcp/requestApproval` | → `mcpServer/elicitation/request` | = the original D3, see §5.3 T2.8 |
| `item/updated` | Upstream no longer has this notification | ✅ **Done**: delete `MethodItemUpdated`, the `ItemUpdatedEvent` struct/alias/decode branch/deref branch; `wait.go`'s `eventMatchesTurn` now relies on the existing `RawNotificationEvent` branch (`wait.go:197`). The old test was rewritten as `TestRemovedNotificationFallsBackToRaw`, asserting the fallback to `RawNotificationEvent` |

> ⚠️ **Important correction**: I5's first version treated `thread/rollback → thread/revert` and `config/update → config/value/write` as "rename migrations". Reading the source confirmed both are **semantic changes**, far more expensive than a rename, and must be designed as new APIs rather than mechanically replaced. `turn/diff` needs a product decision even more.

**Acceptance**: `make conformance-strict`'s `wires-up-but-not-upstream` check is empty. 4 remain today (`config/update`, `thread/rollback`, `turn/diff`, `item/mcp/requestApproval`).

### T1.2 Generate Go protocol types

- **A. codegen (recommended)**: generate params/response structs + method-name constants from the vendored schema (`atombender/go-jsonschema` or a hand-written template); the file header `// Code generated; DO NOT EDIT.` plus a note explaining the coverage determination.
- **B. Hand-write + let T1.1 catch omissions.**
- ⚠️ **Either way: generated constants are never a coverage basis.**

### T1.3 Fix type/modelling defects (D2/D4/D5/D5b)

- ✅ **D2 fixed**: `InitializeCapabilities.OptOutNotificationMethods` → `[]string`; the regression test also asserts that "the old `bool` shape must fail to decode".
- ✅ **D5 fixed**: `InitializeCapabilities` gains `ExplicitGatewayOauth`/`RequestAttestation`/`Extensions`; `ClientInfo` gains `Title`; added `WithClientInfo(name, title, version)`; added a `Version` variable in `options.go` (injectable via `-ldflags -X`, replacing the hardcoded `0.1.0`).
- ✅ **D5b fixed**: no capabilities are announced by default — `InitializeParams.InitializeCapabilities` becomes a **pointer** (`nil` → the field is omitted entirely from the wire, matching upstream's `Option`); `New()` no longer hardcodes `ExperimentalAPI: true`; added `WithInitializeCapabilities` for an explicit override.
- ✅ **D1 follow-up**: `RPCRequest`/`RPCResponse`/`RPCNotification` lose `Version` (the aggregate schema has no JSONRPC envelope type at all, so these three `Version` fields were SDK inventions).
- ✅ **D4 implemented (`c9d4db3`); the original design judgement was correct**: `ServerNotificationEnvelope.emittedAtMs` is **not a member of `params`** — upstream `#[serde(flatten)] notification` + `emitted_at_ms` (`common.rs:2067-2080`) means the wire shape is `{"method":…,"params":{…},"emittedAtMs":123}`, with `emittedAtMs` **sibling** to `method`/`params`. The existing transport only extracts `raw["params"]` (`websocket.go` / `transport.go`'s readLoop), so **sibling fields are discarded at that step**. So D4 must be implemented in the **transport layer** (`Notification` gaining `EmittedAtMs`), not in a decoder — the original task description was wrong and is corrected. Now implemented: `Notification.EmittedAtMs` + `Event.EmittedAtMs` + `ThreadEvent.EmittedAtMs`, sharing `envelopeEmittedAtMs` across the three read loops to prevent drift. **Known limitation**: the stdio transport is jrpc2 underneath, which parses the envelope itself and only exposes method/params, so sibling fields cannot reach the SDK (`EmittedAtMs` is always 0); WS/HTTP are covered.

### T1.4 Strict wire-format alignment (D1 + trace) ✅ done

- ✅ **Delete `"jsonrpc":"2.0"`**: `requestEnvelope`/`notificationEnvelope`/`replyEnvelope` drop `Version`; the `JSONRPCVersion` constant is deleted; every `Version: "2.0"` literal in `transport.go` / `http.go` / `websocket.go` is removed. **No compatibility toggle** (R3).
  - **The one necessary and deliberate exception**: the stdio transport is driven by `jrpc2` (a strict JSON-RPC 2.0 implementation), which both emits and requires that field; its inbound patch `versionFixerReader` (`stdio.go:153-196`) must stay unless jrpc2 is replaced. It is documented in-place in `transport.go` and `stdio.go`.
- ✅ **Add `trace`**: `TraceContext{Traceparent, Tracestate}` (shape from `codex-rs/protocol/src/protocol.rs:168-175`; the type is not in the app-server aggregate schema, so it is modelled locally) + `WithTraceContext(ctx, *TraceContext)` / `TraceContextFromContext(ctx)`; `JSONRPCTransport.Call`, `WebSocketTransport.Call`, `HTTPTransport.Call` inject it automatically. **Carried on requests only** (upstream's `JSONRPCNotification` has no `trace`).
- ✅ **Regression tests**: `internal/transport/wire_shape_test.go` (no `jsonrpc` on the wire, trace only on requests, nil-safe helpers), `internal/protocol/schema/client_types_wire_test.go`.
- ✅ **Real-server verification** (explicitly required by T1.4): `tests/real/wire_alignment_real_test.go` runs an `initialize`+`initialized` handshake **with no `jsonrpc` field** and read-only RPCs against a local `codex app-server --listen ws://…`, and **passes**.

**Acceptance**: no `jsonrpc` on the wire ✅; `trace` injectable ✅; D2/D5 have regression unit tests ✅; a real server accepts the field-less envelope ✅.

---

## 5. WS2 — close protocol capability gaps (stable only)

Common spec: ①`Method*` constant ②request/response type ③actual wiring ④unit test ⑤`docs/api-reference.md` ⑥`llms.txt` ⑦registration in `gen/implemented-methods.json`. **No experimental gating semantics may be introduced** (R2).

### 5.1 reconnect (session supervisor)

- **T2.1 transport**: WS keepalive (`conn.Ping` + timeout); `ReconnectingWS` fixes `Call` hitting a dead connection during the re-dial window.
- ✅ **T2.2 session rebuild (implemented, with one deviation from the original design)**: add `sessionSupervisor` (`supervisor.go`) — listen for the **transport-layer reconnect signal** → **re-run `initialize`+`initialized`** → call `thread/resume` for every thread in the **expected subscription set**; provide `WithAutoReconnect`.
  - ⚠️ **Deviation from the original design**: the original said "listen for `Done()` → re-dial", but `ReconnectingWS.Done()` **only closes on permanent shutdown**, not on a dropped connection (re-dial is transparent). So instead the transport layer gains **`Reconnects() <-chan struct{}`** (one signal per successful re-dial, merged with cap=1) that the supervisor consumes. This also corrects a conceptual error: **"re-dial" ≠ "session recovery"** — re-dial only restores the socket, whereas the app-server treats a new connection as a **brand-new client**; without replaying the handshake, requests keep going out on a connection the server has no session for (worse, they may "look successful" against an empty session).
  - ✅ `WithAutoReconnect` **fails loudly** when the transport **cannot report reconnects** (rather than silently doing nothing), so callers do not believe the session is being recovered.
  - ✅ **Event-gap backfill implemented**: `sdk/sessionBackfilled` carries **the authoritative history of each resumed thread** (`thread/turns/list`). It is **deliberately "history", not "a replay of missed notifications"** — emitting wire-shaped events would be indistinguishable from live notifications, and **a consumer that had already received part of a turn before the drop would double-count**; the contract is "merge by turn id". Default 20 turns per thread; `WithSessionBackfill(n)` adjusts it, `0` disables it.
    - ⚠️ Adopting `thread/turns/list` exposed a **real bug**: `schema.Turn` typed `startedAt`/`completedAt` as `*time.Time`, while **upstream sends an int64 Unix second** ⇒ **real turns failed to decode**, and backfill would never fire for any turn with a timestamp. The original test fixture happened to have **no timestamps**, so it was not exposed. Fixed (`Turn` switched to generated) and a real-timestamp assertion added.
- **T2.3 in-flight operations**: fix `retry.go:125-127` (`ErrClosed` being non-retryable caused total hard failure at the instant of a drop); distinguish read-only retryable from non-retryable writes; `Notify` and server-request replies are not retried.
- ✅ **T2.4 approvals across reconnects (implemented)**: `requestLoop` previously used `_ = req.Reply(...)`, **discarding every reply error** — a failure the **handler itself cannot notice**: it may already have decided (even approved a command) while the decision is discarded. It now checks the reply error and emits `sdk/pendingApprovalLost` (with the method and a best-effort thread/turn id); a **second reply** to the same request is filtered (that is a programming error, not a "lost" one).
  - ⚠️ **Coverage caveat (documented at the call site)**: the stdio transport is jrpc2 underneath, which hands replies to its own channel, so a lost reply there can present as a **normal return**. So detection is reliable over WebSocket/HTTP, and **best-effort** over stdio.
  - ✅ **Configurable approval timeout implemented**: `Dispatcher.ApprovalTimeout` (default 0 = wait forever, suitable when "the approval comes from a human"). On timeout the SDK **answers on the handler's behalf**, with **exactly the same** answer as "no handler configured" (always refuse) and reports `timedOut` ⇒ **a timeout never grants anything** (an invariant pinned by a test). Applied to all 5 server-request handlers (permissions / user input / exec / file change / elicitation).
- ✅ **T2.5 observability events (done)**: `ReconnectStartedEvent` / `ReconnectSucceededEvent` (with `ThreadsResumed`/`ThreadsFailed`) / `ReconnectFailedEvent` (with `Err` and `Attempt`) / `SessionRecoveredEvent` (with the **concrete thread id list**) / `UnhandledServerRequestEvent` / `PendingApprovalLostEvent`. **Note**: these are **not wire notifications** (upstream has no such methods), so they are delivered to the same `EventSubscription` under **synthetic method names** with an `sdk/` prefix, so a single consumer loop can handle everything and the prefix keeps them from being confused with real notifications.
  - ✅ `EventsLostError`/`EventsLostEvent` gain **`GapFrom`/`GapTo`**: they bound the time window of dropped events. **Deliberately timestamps rather than sequence numbers** — upstream notifications **carry no sequence number**, so "name the gap by position" is impossible to derive; time is **derivable** and sufficient to correlate a loss with what else happened. The window covers only events **still queued at termination** (events already handed to the consumer do not count as lost — the test pins this explicitly; my first assertion wrongly counted an **already-delivered** event).
  - ✅ **Gaps across a reconnect are covered by backfill** (above); upstream has no replay, so backfill is the only means.


- **T2.6 Event delivery: no silent drop, bounded close, observable (timeout defaults to 5s) — redesigned per A2/A3/A8**

  **Invariant goals**: no silent drop; bounded close; observable loss.
  **Two hard constraints**:
  - **C1: `publish` never waits for a subscriber under any circumstance.** Waiting on the shared publish path for a slow subscriber would drag the path out by O(#slow subscribers × 5s); adding a forwarding goroutine or releasing locks **does not** fix it — the wait must happen only on the **subscriber's own execution path**.
  - **C2: the terminal notification must have a delivery channel that does not depend on the consumer reading**; and **the in-band terminal event must genuinely reserve a slot** (merely declaring "reserved" while writing directly with `select` fills the channel, making the reservation meaningless).

  **Structure (one per subscriber)**:
  ```
  backlog   bounded deque, maxBacklogEvents (default 4096)
  out       chan Event, cap = outCap (default 128, must be >= 2)
  terminal  atomic { reason: "stall"|"overflow"|"closed", since, lostCount }
  stop      chan struct{}   // terminal broadcast: closed once by the winner on the first terminal set (sync.Once)
  done      chan struct{}   // out-of-band terminal signal: closed by the forwarding goroutine in its exit path
  notify    chan struct{}, cap 1   // coalesced wake signal (only means "there are new events")
  ```

  **`publish(ev)` (never blocks)**: for each subscriber — take a short lock; skip if already terminal; if `len(backlog) >= maxBacklogEvents` → set `terminal(overflow)` (counts into `lostCount`, and the winner does `close(stop)`) then skip; otherwise append; unlock; non-blocking send to `notify`. Cost is O(#subscribers), only append + atomic, **no timeout wait at all**.

  **Wake-up and exit (fixes A9; critical)**:

  - **There are three classes of `terminal` setter**: `Close()` (`"closed"`), a `publish` overflow (`"overflow"`), and the forwarding goroutine's own stall (`"stall"`). **Every setter must broadcast via a `sync.Once`-protected, close-once `stop` channel.**
  - **Both wait points of the forwarding goroutine must `select` on `stop`**: the idle wait `select { case <-notify: case <-stop: }` (**it cannot wait on `notify` alone**); the delivery wait `select { case out <- head: …; case <-time.After(remaining): …; case <-stop: … }`.
  - **Why `notify` alone is not enough**: `notify` is a cap=1 **coalescing** signal, so a token may already be pending (the non-blocking send fails and nobody gets woken). So in the "subscribe and receive nothing, then `Close()`" scenario, without `stop` the forwarding goroutine would **block forever** on `notify` → `out` never closes, `done` never closes → the consumer's `for range sub.C()` and `<-sub.Done()` **hang forever** (goroutine leak). `close(stop)` is a **reliable wake-up** for a goroutine blocked on that channel, independent of the coalescing signal's state.
  - **Division of exit responsibility**: `Close()` only 「CAS-sets `terminal("closed")` + `close(stop)`」 and **returns immediately** (no waiting, no blocking, idempotent); **`close(out)` and `close(done)` are performed only by the forwarding goroutine in its exit path**, guaranteeing no write after `close(out)` (avoiding a send-on-closed panic). `stop` and `done` are each `sync.Once`-protected; calling `Close()` after stall/overflow has fired must be idempotent and not panic.
  - **Order**: set terminal → `close(stop)` → the goroutine observes `stop`/`terminal` → write the reserved terminal event → `close(out)` → `close(done)`. The consumer perceives termination via `C()` (closed, or a terminal event) **or** `Done()`/`Err()`, neither of which depends on the consumer reading `C()`.

  **Forwarding goroutine (one per subscriber; waits happen only here) — with slot reservation (fixes A8) + reliable exit (fixes A9)**:

  ```
  stallDeadline: time.Time  // zero value = not yet stalled
  for {
      if terminal != nil { break }
      if len(backlog) == 0 {
          select { case <-notify: case <-stop: }   // must be wakeable by stop
          continue
      }
      head := backlog[0]

      // Key: an ordinary event occupies strictly <= outCap-1, leaving the last slot for the terminal event
      if len(out) >= outCap-1 {
          if stallDeadline.IsZero() { stallDeadline = now() }
          if since(stallDeadline) > timeout { setTerminalOnce("stall"); break }
          select {                                  // wait (bounded) for consumer progress, also wakeable by stop
          case <-time.After(pollInterval /* 50ms */):
          case <-stop:
          }
          continue
      }
      select {
      case out <- head:
          pop(backlog); stallDeadline = zero      // consumer progress -> reset
      case <-time.After(remaining(stallDeadline, timeout)):
          setTerminalOnce("stall"); break
      case <-stop:
          break
      }
  }
  // terminal delivery (only this goroutine does it; observing stop implies terminal is set)
  if terminal != nil {
      out <- terminalEvent   // guaranteed to succeed immediately: ordinary events occupy at most outCap-1, so the last slot is free
  }
  close(out)
  close(done)                // out-of-band (authoritative)
  ```

  ⚠️ **Implementation note (easy to get wrong)**: in Go, `break` inside `select` **only leaves the `select`, not the `for`**. The `<-stop` branches above must use a **labelled break** (`break loop`) or an exit flag, otherwise it spins once `terminal` is set. `setTerminalOnce` must also be idempotent (`sync.Once` or CAS).

  **Reservation correctness argument**: the **only writer** of `out` is this forwarding goroutine; the guard `len(out) >= outCap-1` ensures occupancy after an ordinary send is ≤ `outCap-1`; readers only decrease `len`. Therefore the `outCap`-th slot is **always free**, and the terminal event's send **always succeeds immediately** (non-blocking semantics). ⚠️ `outCap` must be ≥ 2.

  **How to wait for consumer progress once capacity is exhausted**: take the `len(out) >= outCap-1` branch, polling at `pollInterval` (50ms) with `stallDeadline` tracking the total timeout; when consumption resumes (`len` drops) it returns to the normal path and resets `stallDeadline`; continued lack of progress → `terminal("stall")`.

  **Two-channel terminal delivery (fixes A3)**:
  1. **Out-of-band (authoritative, guaranteed)**: `EventSubscription.Err() error` + `Done() <-chan struct{}`. Even if the consumer **never reads** `C()`, the terminal state is visible and `done` closes → this is the "bounded close + observable loss" guarantee.
  2. **In-band (guaranteed by the reservation)**: the terminal event is written to the reserved slot, then `close(out)`; if the consumer never reads, the event sits in the buffer and **blocks no goroutine**.

  **Semantics summary**: the normal path has no loss and `publish` does not block; the abnormal paths (stall / backlog overflow / explicit Close) converge via `Err()`/`Done()` + the reserved slot, with `lostCount` reflecting the drop count. **After `Close()` returns, the forwarding goroutine is guaranteed to exit within a bounded time and close `out`/`done`** (it relies on the `stop` broadcast, **independent of whether any event ever occurred**). **No** optional policy toggle. `Close()` is idempotent, concurrency-safe, non-blocking.

  **Required tests**:
  - **Idle subscriber closes directly (per A9)**: subscribe, **publish nothing**, `Close()` directly → assert ① `C()` closes within a bounded time (`for range` exits, does not hang) ② `Done()` closes ③ `Err()` is non-nil with `reason == "closed"` ④ no goroutine leak (compare the goroutine count before/after, or fall back to a timeout assertion).
  - **`Close()` concurrent with termination**: call `Close()` after stall/overflow has fired (`stop` already closed) → idempotent, no panic, no double close; and `reason` keeps the first set value.
  - **Consume nothing at all + deliver ≥ `outCap` ordinary events (per A8)**: assert ① ordinary occupancy ≤ `outCap-1` ② the terminal event **can be written immediately** (assert with `select`+`default` that it enqueues instantly) ③ `Err()` is non-nil and `Done()` has closed ④ `publish` never blocked throughout (within `maxBacklogEvents`).
  - **Consumption resumes**: after the consumer starts reading again, ordinary events resume and no stall is falsely triggered; `stallDeadline` is correctly reset.
  - **Only 1 of several subscribers is slow**: the others' delivery latency is unaffected.
  - **`publish` latency does not grow with the saturation duration**: the p99 difference before/after saturation is within a threshold.
  - **Backlog overflow**: `reason == "overflow"` and `lostCount > 0`, and the goroutine exits promptly after the overflow too.
  - Under `-race`, concurrent `Close`/`publish`/forwarding goroutine show no data race.

### 5.2 filesystem (9 methods + 1 notification, currently 0)

- `fs/readFile`, `fs/writeFile`, `fs/createDirectory`, `fs/getMetadata`, `fs/readDirectory`, `fs/remove`, `fs/copy`, `fs/watch`, `fs/unwatch`; notification `fs/changed`.
- `fs/readFile` returns base64 → provide `ReadFileBytes()`.
- `fs/watch` is stateful: automatically `fs/unwatch` on `Client.Close()` / thread end; route `fs/changed` by watch id.
- Upstream: `protocol/v2/fs.rs`.

### 5.3 MCP lifecycle (5 methods + 1 ServerRequest + 2 notifications, currently 0)

- Client methods: `mcpServer/oauth/login`, `config/mcpServer/reload`, `mcpServerStatus/list`, `mcpServer/resource/read`, `mcpServer/tool/call`
- ServerRequest: `mcpServer/elicitation/request`
- Notifications: `mcpServer/oauthLogin/completed`, `mcpServer/startupStatus/updated`
- T2.8: add `McpElicitationHandler`; **delete** `item/mcp/requestApproval` (`envelope.go:46`, `interaction.go:164,218`, `decode.go:34`, `sdk_v2_test.go:2200`) — per R3, leave no old name.
- Upstream: `protocol/v2/mcp.rs`.

### 5.4 remote control — **out of scope per R2 (confirmed abandoned)** ❌

All 7 `remoteControl/*` methods are experimental (`common.rs:1155-1193`). Keep the `RemoteControlStatusChangedEvent` notification type, and document that the RPC surface is not provided; no exception is restored.

### 5.5 plugins / marketplace (15 methods, currently 0)

- `marketplace/` (3): `add`, `remove`, `upgrade`
- `plugin/` (12): `list`, `installed`, `reconcile`, `read`, `skill/read`, `share/save`, `share/updateTargets`, `share/list`, `share/checkout`, `share/delete`, `install`, `uninstall`
- ⚠️ Variants marked `serialization: global("config")` (`plugin/install|uninstall`, `marketplace/*`, `plugin/share/*`) are **global-config-level changes**, serialized server-side (`ClientRequestSerializationScope`, `common.rs:129-206`); the docs must state that concurrent calls are queued.
- Read paths first, then write paths.

### 5.6 Other stable gaps (by delivery order, **full delivery**)

> ⚠️ Revision A5: every method in this section **must** be delivered in the final release. P1/P2/P3 indicate **delivery order** only.

| Order | Group | Count | Note |
|---|---|---:|---|
| P1 | `thread/turns/list`, `thread/items/list` | 2 | required for reconnect backfill |
| P1 | `account/chatgptAuthTokens/refresh` | 1 | ServerRequest; without it, an expired auth in a long session cannot recover |
| P1 | `mcpServer/elicitation/request` | 1 | see §5.3 |
| P1 | `account/*` stable completion | 8 | 12 stable − 4 implemented |
| P2 | `thread/attachment/*` | 4 | + notification `thread/attachment/updated` |
| P2 | `threadSection/*` | 4 | `list/create/update/delete` |
| P2 | `app/*` | 3 | + notification `app/list/updated` |
| P2 | `thread/revert` | 1 | |
| P3 | `permissionProfile/list`, `configRequirements/read` | 2 | |
| P3 | `feedback/upload` | 1 | |
| P3 | `externalAgentConfig/*` | 4 | `detect/import/import/recordHistory/import/readHistories` |
| P3 | `windowsSandbox/*` | 2 | `setupStart`, `readiness` |
| P3 | `fuzzyFileSearch` | 1 | the basic method only |
| — | notifications | reach **61** | implement to 61 per §5.7 (the existing 65 typed are based on the old schema and must be re-baselined) |

**Acceptance**: every method in the table above is implemented, wired, tested and registered; notifications reach 61. **The only allowed unimplemented entries are §5.7's whitelist (5, see §0.3 I11).**

### 5.7 Non-implementation baseline (**5 entries**, CI whitelist — was 8, see §0.3 I11)

> **Scope clarification**: the whitelist contains only methods that are **"within `declared_stable` but not implemented this round"** (**5 after implementation**; the draft listed 8, of which 3 became real wiring during implementation, see I11). The 89 experimental entries are **out of scope because they are not stable** — a different class, **not counted in the whitelist** (otherwise the gate would make a meaningless deduction for non-scope entries).

**Whitelist A — v1 deprecated (R4, 3 ClientRequest)**: `getAuthStatus`, `getConversationSummary`, `gitDiffToRemote`
(Note: these 3 are **also excluded from the export**, see §2.2; they are still in `declared_stable`, hence registered.)

**Whitelist B — R4 server-initiated (1 after implementation)**:

| Method | Disposition |
|---|---|
| `attestation/generate` | D5b does not announce `requestAttestation` by default; **inbound replies `-32601` per §5.8 + records** |
| ~~`applyPatchApproval`~~ | ✅ **Removed from the whitelist**: `interaction.go` performs a protocol-valid decline per §5.8 (real wiring), so it is registered as implemented (see I11) |
| ~~`execCommandApproval`~~ | ✅ **Removed from the whitelist**: as above |

**Whitelist C — internal-only notifications (1 after implementation)**: `rawResponse/completed`
(present in the source but **excluded from the export**; not exposed to clients; no decoder implemented.)
> `rawResponseItem/completed` **was removed from the whitelist**: it already has a typed decoder (`RawResponseItemCompletedEvent` in `events_extra.go`), i.e. real wiring, so per the gate's "a whitelist entry must not be contradicted by wiring" it is registered as implemented (see I11).

**Non-scope set (a different class, registered in `gen/not-in-scope.txt`)**: the 89 experimental entries = 65 ClientRequest + 1 ServerRequest (`currentTime/read`) + 23 notifications. Basis: source `#[experimental("...")]` annotations.

### 5.8 Policy for unimplemented / unconfigured server-initiated requests (T2.13) ✅ implemented (with one correction to the plan)

> ⚠️ **Correction: the plan's original answer value `"denied"` is wrong.** Upstream's `ReviewDecision::Denied` is a **struct variant** (`Denied { rejection: String }`), which under serde's external tagging serializes to **`{"denied":{"rejection":"denied"}}`**, **not** the bare string `"denied"` — no unit variant matches the bare string, so the server would reject it, turning a recoverable deny into a protocol error. Verified against the exported schema (`schema/json/ApplyPatchApprovalResponse.json`) and implemented; the test also asserts that the bare-string form must **not** appear, nor may `abort` (which would abort the session).
>
> ⚠️ **Correction 2: the gate's `server_request_handler` criterion was too broad.** T1.1's text said "`Dispatcher.HandleServerRequest` has a `case`", but the implementation also counted `internal/protocol/decode.go` as evidence — whereas "can decode the params" does not mean "someone handles the request". Tightening it to accept only `interaction.go` exposed **2 methods previously reported as implemented but not**: `item/permissions/requestApproval` and `item/tool/requestUserInput` have **only decode branches, no dispatch branch at all** (`grep` confirms zero occurrences in `interaction.go`). The gap therefore went from 0 to **2** (the true state).



**Goal**: not implementing a server→client request **must not terminate the session/turn**. Externally it is a normal protocol reply; internally it leaves an observable record.

**Current problem**: `Dispatcher.HandleServerRequest`'s default returns `protocol.ErrUnsupportedServerRequest` (`interaction.go:232-237`) → `requestLoop` replies with a JSON-RPC error `-32603` (`client.go:574-576`) → the server may terminate the turn.

| Case | External reply | Internal |
|---|---|---|
| A known approval-class request with no handler configured (including whitelist B's two legacy methods, and `d.Exec/File/MCP == nil`) | **A protocol-valid deny/decline**, no JSON-RPC error | record + event |
| Unknown method (cannot construct a valid response body) | `-32601 Method not found` | record + event |
| Implemented and the handler works | normal reply | — |

- **The two legacy methods**: the response is `{ decision: ReviewDecision }` (upstream `protocol/v1.rs:156-180`). Take **`"denied"`**, whose semantics are *"…should not execute it, **but it should continue the session and try something else**"*.
  - ⚠️ v1 `ReviewDecision` is **snake_case** (`approved`/`approved_for_session`/`denied`/`timed_out`/`abort` + two payload-carrying objects); it **must not** reuse v2's `accept`/`decline`/`cancel`.
  - ⚠️ **Do not** use `"abort"` (it aborts the turn).
- **Observability**: `UnhandledServerRequestEvent{Method, ThreadID, TurnID, Action, Reason}` is delivered via `client.Events()` + logged.
- **No** policy toggle.
- **Regression test**: an inbound `applyPatchApproval` with no handler configured → assert ① the reply is `{"decision":"denied"}` ② no JSON-RPC error ③ the turn/session continues ④ the event is emitted.

---

## 6. WS3 — update README.md, examples and docs

- **T3.1 README**: the coverage table is **auto-generated** (`implemented-methods.json` against `method-surface.json`), with a header noting "aligned to codex `14c8b777`, scope=stable"; fix the D6 version inconsistency; state the three policies (R2/R3/R4) and link `gen/not-implemented.txt` and `gen/not-in-scope.txt`; add "upstream does not use `jsonrpc`"; add a Reliability section (reconnect boundaries + event-delivery semantics and `Err()`/`Done()` usage).
- **T3.2 examples**: `reconnect-supervisor/`, `approval-over-websocket/`, `fs-and-mcp/`, `streaming-to-sse/` (demonstrating `Err()`/`Done()` and correct consumption under backpressure).
- **T3.3 docs**: `docs/index.md` (reconnect + delivery semantics + transport table), `docs/api-reference.md` (new methods/types), `llms.txt`+`llms-full.txt` (fix D7, add the inventory, note the baseline), add `docs/reconnect.md`; clean up the stale `PHASE3_REVIEW.md`.
  - ✅ **Method lists are now generated** (added during implementation): the Client/SessionThread method lists in `docs/api-reference.md` and `llms*.txt` are no longer hand-written; the Go tool `tools/gendocs` generates them by parsing the exported surface with `go/ast` (`make docs`), and `make docs-check` verifies the lists match the code and is folded into `conformance-strict`. Same rationale as the README coverage table: hand-written content describing machine-checkable data will always rot. Go rather than Python, so no Python toolchain needs to be introduced for this.

**Acceptance**: the examples `go build` and are compiled by CI; the README numbers match the conformance report.

---

## 7. WS4 — automated acceptance and CI regression prevention

- **T4.1** `make sync`: T0.1 + T0.4's six assertions.
- **T4.2** `make conformance`: T1.1 produces all `gen/*` files.
- **T4.3** CI gates:
  - `declared_stable − whitelist − implemented ≠ ∅` → fail (**check only the implemented set, not generated constants**).
  - Any of T1.1's three anti-fraud checks fails → fail.
  - Any of T0.4's six reconciliation assertions fails → fail and require human review (to prevent a silent absorption of changed upstream export semantics).
  - `gen/export-exclusions.json` or the whitelist changes → prompt for review.
  - `gen/conformance-report.md` is stale → fail.
- **T4.4** Unit tests: D2/D4/D5 regressions; reconnect disconnect-recovery; **backpressure and exit specials** (including "consume nothing at all + ≥ outCap ordinary events", "**subscribe and publish nothing, then Close directly**", "Close concurrent with stall/overflow is idempotent", `publish` latency not growing with saturation, goroutine-leak checks, `-race`); §5.8 decline regression.
- **T4.5** Real environment: `tests/real/` gains reconnect, fs, mcp, and "still works after removing `jsonrpc`".

---

## 8. Milestones and delivery order

> ⚠️ **Milestones are a delivery order, not a partial scope.** Each milestone's exit condition is that **the corresponding method set is 100% done and registered**; there is no "P2 does only 60%".

| Milestone | Delivered set (all done before exit) | Depends on | Estimate |
|---|---|---|---|
| M0 | WS0: sync infrastructure + T0.4's six assertions + T1.1/T1.1b tooling and the 43-item audit | — | medium |
| M1 | WS1: all of T1.2/T1.3/T1.4 fixed and aligned | M0 | medium |
| M2 | §5.2 fs (9+1) + §5.3 mcp (5+1+2) | M1 | medium |
| M3 | §5.1 reconnect (T2.1–T2.6) + §5.6 P1 (12) | M1 | **large** |
| M4 | §5.5 plugins/marketplace (15) | M1 | medium |
| M5 | §5.6 P2+P3 (15) + notifications' 61 target | M2/M4 | medium |
| M6 | WS3 docs/examples + WS4 CI | parallel with M2–M5 | small-medium |

Order: **M0 → M1 → M2 (fs/mcp) → M4 (plugins) → M3 (reconnect) → M5 → M6**.

---

## 9. Risks and trade-offs

| Risk | Note | Mitigation |
|---|---|---|
| R1 no version anchor | upstream `0.0.0` | Anchor on commit(`14c8b777`)+sha256 |
| R2 experimental out of scope | ✅ decided. Side effect: the whole remote-control group is unavailable | `gen/not-in-scope.txt` on record; prompt on change |
| R3 no compatibility for old names | ✅ decided. Breaking change | CHANGELOG + README note; major release |
| R4 whitelist 5 entries (was 8, see I11) | ✅ decided | Explicit whitelist registration + reasons |
| R9 degrade unimplemented requests | ✅ decided. An unknown method can only reply `-32601` | decline path + event trace |
| **R10 scope authority misplaced** (fixed) | The previous version wrongly treated the export as authoritative → it would count "the 3 export-excluded methods" as gaps and miscompute the notification scope | Scope is now defined by source non-experimental; the export is only for reconciliation |
| **R11 difference-set drift** | The export exclusion set or the notification filtering behaviour may change | T0.4 assertions 1/4/6 make that fact explicit; a change fails and requires human review |
| **R12 reserved slot not honoured** (fixed) | Merely declaring "reserved" while writing directly with `select` fills the channel | Guard with `len(out)` to cap ordinary events at `outCap-1`; a dedicated test covers "consume nothing" |
| R5 codegen cost | 270+ type files | Generate only method-name constants + gap-group types first |
| R6 reconnect is not transparent | No replay upstream; approvals are inevitably lost | Document as "at-least-once + application-level idempotency" |

---

## 10. Definition of Done

1. `make sync CODEX_SRC=<codex checkout>` (i.e. `scripts/codex_schema_surface.py sync`) is idempotent; `.codex-schema/manifest.json` records `14c8b777` and sha256; `version.go` is written by the generator.
2. There is no manually cropped `v2.schema.json`.
3. `gen/method-surface.json`, `gen/export-exclusions.json` (5), `gen/not-in-scope.txt` (89), `gen/not-implemented.txt` (5, a read-only mirror of `gen/whitelist.json`) are all present; **T0.4's six set assertions all pass**.
4. `declared_stable − whitelist(5) − implemented == ∅`, and T1.1's three anti-fraud checks pass (**coverage is judged on real wiring, not generated constants**); **kind covers all four faces and face↔kind is surjective** (`initialized` is registered legally as `client_notification_sender`); T1.1b's 43-item audit is complete and the leftovers are cleaned per R2/R3.
5. **D1–D7 all fixed**, leaving no old names / compatibility branches per R3 (including `item/mcp/requestApproval` and the `jsonrpc` field).
6. reconnect: after a disconnect it automatically completes re-dial → initialize → thread/resume → backfill of results during the gap; with automated tests.
7. **Event-delivery semantics met**: `publish` never blocks (saturation specials + quantitative assertions); ordinary occupancy ≤ `outCap-1`, the terminal event **written immediately** via the reserved slot; `Err()`/`Done()` guaranteed; **the forwarding goroutine always exits after `Close()`** (including the "idle subscriber closes directly" and "Close concurrent with termination" tests, with no goroutine leak); no silent-drop path.
8. **Full gap delivery**: ClientRequest 62, ServerRequest 2, all of §5.6 P1/P2/P3, notifications' 61 target — all implemented, wired, tested and registered. **The only allowed unimplemented entries are the whitelist's 5 (see §0.3 I11).**
9. An unimplemented server-initiated request does not terminate the session (§5.8 regression test passes).
10. README/docs/llms/examples are updated, the examples compile, and the coverage numbers are auto-generated.
11. CI is all green, and the gates fail on protocol drift or changed export semantics.

---

## 11. Resolution log (all closed)

| # | Topic | Resolution |
|---|---|---|
| 1–6 | (First round) timeout behaviour / abandoning remote control / skipping v1 deprecated / not implementing attestation / 5s timeout / silent decline | see §0 Decision Log |
| 7 | Review A1: coverage-gate false positive | Adopted: declare/implement set separation + three anti-fraud checks |
| 8 | Review A2: the forwarding goroutine still blocks the shared path | Adopted: `publish` never blocks |
| 9 | Review A3: no delivery mechanism for the terminal event | Adopted: out-of-band `Err()`/`Done()` + reserved slot |
| 10 | Review A4: the stable export is not a pure stable set | Adopted: scope defined by source classification |
| 11 | Review A5: per-phase acceptance conflicts with the DoD | Adopted: unified full delivery |
| 12 | Review A6: the T0.4 assertion fails at the current baseline; 5 export exclusions | Adopted: register the 5; the assertion becomes **set equality**; **withdraw the "counting error" conclusion** (R11) |
| 13 | Review A7: gap 59 double-subtracted legacy | Adopted: set-difference computation → gap **62**; add T1.1b auditing the 43 (R10) |
| 14 | Review A8: the forwarding pseudocode did not implement the slot reservation | Adopted: `len(out)` guard + wait for consumer progress + a "consume nothing" test (R12) |
| 15 | Review A9: an idle subscriber's `Close()` may never exit | Adopted: one-shot `stop` broadcast + both wait points select on `stop` + division of exit responsibility; add an "idle direct Close" test (R13) |
| 16 | Review A10: the registry missed the ClientNotification send path | Adopted: add `client_notification_sender` + face↔kind surjection check (R14) |
| 17 | Implementation I1: the anchor was wrongly set to the CLI | Adopted: anchor = **a codex repo commit**; the CLI is only the `diff-cli` drift guard (measured 0.160.0 is one method + one notification behind) |
| 18 | Implementation I2: `GeneratedAt` breaks idempotency | Adopted: `version.go` contains no timestamp, only deterministic values |
| 19 | Implementation I3: vendor target and reason evidence | Adopted: vendor the aggregate stable schema + derived method sets; the internal-only exclusion reason is now backed by an upstream comment |
| 20 | Implementation I5's `item/updated` | Adopted: delete the notification type and decode branch; the test now asserts the fallback to `RawNotificationEvent` |
| 21 | **`turn/diff` disposition (user decision)** | JSON-RPC has no `turn/diff` → **the SDK does not implement it**. Delete `Client.TurnDiff`/`SessionThread.GitDiff`/`TurnDiffRequest`/`TurnDiffResult`/`MethodTurnDiff` and the related doc references; keep the `TurnDiffUpdatedEvent` notification |
| 22 | **Type-level cleanup (user decision: clean up before advancing)** | Adopted and landed three principles: **① eliminate benign naming differences** (16 `*Request`/`*Result` → upstream `*Params`/`*Response`; `Capabilities` → `InitializeCapabilities`; `SchemaItem/Turn/Thread` → `ThreadItem/Turn/Thread`); **② invent no types** (delete `RPCRequest`/`RPCResponse`/`RPCNotification`/`RPCError`/`InitializedNotification` and the unused aliases; the wire envelope belongs to `internal/transport`); **③ delete the real orphan** (`ThreadRollbackRequest`, and migrate `thread/rollback` to the really-existing `thread/revert`). Added the type-level gate `scripts/type_check.py` + `gen/type-allowlist.json` (I10) |
| 23 | **`TokenUsage` disposition** | **No rename this round, left not whitelisted**: I9 shows it is an outdated usage model (usage moved to `thread/tokenUsage/updated` + `ThreadTokenUsage`), and renaming would create a false impression of "aligned". Keep alarming until it is refactored to its correct shape |

> No open items remain.

---

## Appendix A: Scope summary (the only machine-readable source is `gen/*.json`)

> This appendix no longer lists methods by hand — hand-written lists have introduced deviations several times (A4/A6). **The authority is the `gen/method-surface.json` + `gen/export-exclusions.json` produced by T0.4.**

| Face | Source total | experimental (out of scope) | declared_stable | Whitelist | Implemented | **To implement** |
|---|---:|---:|---:|---:|---:|---:|
| ClientRequest | 173 | 65 | 108 | 3 | 105 | **0** |
| ServerRequest | 11 | 1 | 10 | 1 | 9 | **0** |
| ServerNotification | 86 | 23 | 63 | 1 | 62 | **0** |
| ClientNotification | 1 | 0 | 1 | 0 | 1 | 0 |
| **Total** | **271** | **89** | **182** | **5** | **177** | **0** |

> The whitelist narrowed from 8 to 5 (see §0.3 I11); "Implemented / To implement" are the **post-implementation** measured values (`gen/implemented-methods.json`), not the pre-work baseline.

Consistency: `173−65=108`; `108−3=105`; `11−1=10`; `10−1=9`; `86−23=63`; `63−1=62`; `84 (export stable notifications)=63+23−2`; `105 (export stable client)=108−3`; `170−105=65` (= source experimental); `182−5−177=0`.

---

## Appendix B: Evidence index

| Conclusion | Evidence path / command |
|---|---|
| No protocol version number | `codex-rs/Cargo.toml:163`, `codex-rs/app-server-protocol/src/rpc.rs:11` |
| Does not use the `jsonrpc` field | `codex-rs/app-server-protocol/src/rpc.rs:1-2,45-79` |
| **Source method surface** (173 / 11 / 86 / 1) | `codex-rs/app-server-protocol/src/protocol/common.rs:499-1510,1780-1851,1935-2061,2082-2084` |
| **Export method surface** (105/170, 10/11, 84/84, 1/1) | decompress `schema/precomputed/app-server-exports-{stable,experimental}.json.zst` → `json_schema[*]["method"].enum` |
| **The 5 export exclusions** | the source set above − the export (experimental) set; the 5 are absent from both exports (re-checked) |
| Notifications are not filtered by experimental | `export_stable(ServerNotification) == export_experimental(ServerNotification)` (set-equal) |
| The 89 experimental entries | `grep -o 'experimental("\([^"]*\)")' common.rs \| sort` (89 = 65 + 1 + 23) |
| `remoteControl/*` all exp | `common.rs:1155-1193` |
| `mcpServer/event/stream/*` exp | `common.rs:1257-1266`, `2001-2002` |
| `plugin/search` exp | `common.rs:916-917` |
| `InitializeCapabilities` fields | `codex-rs/app-server-protocol/src/protocol/v1.rs:29-70` |
| `ReviewDecision` (v1, snake_case) | `v1.rs:156-180` + `.codex-schema/ApplyPatchApprovalResponse.json:100-119` |
| Notification envelope `emittedAtMs` | `common.rs:2063-2080` |
| No HTTP/SSE, only stdio/unix/ws | `codex-rs/app-server-transport/src/transport/mod.rs:80-166` |
| WS frame-level ping | `codex-rs/app-server-transport/src/transport/websocket.rs:363-373` |
| Subscriptions are per connection | `codex-rs/app-server/src/thread_state.rs:344-348,433`; `thread_processor.rs:3605` |
| No client event replay | `codex-rs/app-server/src/transport.rs:204-243` |
| `thread/resume` = the replay replacement | `codex-rs/app-server/src/thread_state.rs:59-64` |
| precomputed decompression/distribution / fixtures | `precomputed_exports.rs:15-18`; `schema_fixtures.rs:95-153` |
| schema generation CLI / `--experimental` | `codex-rs/cli/src/main.rs:727-735,1421-1425` |
| The SDK sends `jsonrpc` | `internal/transport/transport.go` (`requestEnvelope`/`notificationEnvelope`) |
| The SDK's pinned subset schema | `internal/protocol/schema/v2.schema.json`, `version.go:3-10` |
| SDK method constants (**not usable as a coverage basis**) | `internal/protocol/envelope.go:12-168` |
| The SDK's old MCP approval method | `internal/protocol/envelope.go:46`, `interaction.go:164,218`, `decode.go:34` |
| The SDK dispatcher's default error branch | `interaction.go:232-237`, `client.go:574-576` |
| The SDK's silent backpressure drop (while iterating under a lock) | `events.go:370-385` |
| The SDK's `ErrClosed` not retryable | `internal/transport/retry.go:125-127` |
| The SDK's sync-script path is dead | `scripts/update-codex-go-schema.sh:6-7` |
| Doc inconsistencies | `llms.txt:2-5`, `README.md:9,40,106`, `VERSION` |

---

## Wrap-up actions (user-decided, **to be executed after the plan is complete**)

### C1. Squash all commits into one, to remove two binaries from history

**Background**: `e2e.test` (4.4 MB) and `simple` (6.4 MB) were once committed to the repo and were removed from the **index and working tree** in `aff0245`. But **both blobs are still in history** (introduced by the initial commit), so the size of existing clones does not shrink — which is what this addresses.

**Approach**: once all the plan's work is done, squash the branch's **entire commit list into one**. The squash creates a **new root commit** whose tree never had those two files, so they become **unreachable** in the new history and a fresh clone will not download them. Equivalent approaches are `git checkout --orphan` followed by a single commit, or folding with `git rebase -i --root`.

**Prerequisites and consequences that must be handled together**:

| Item | Note |
|---|---|
| **Rewrites history** | The squash changes **every commit's hash** ⇒ any **existing clone / open PR / someone else's local branch** is invalidated, requiring a fresh clone or hard reset |
| **Remote objects do not vanish immediately** | The old blobs remain reachable objects on the remote until it runs GC (GitHub usually needs a trigger, or its automatic GC to run). To reclaim immediately, contact the host or wait |
| **There must be no unpushed dependents first** | If anyone is already working on top of this branch, notify them first; **never force-push without their knowledge** |
| **Do not discard work before squashing** | The squash is the **last step**; once done, you can no longer use a single commit to trace a change back |

**Why it is worth it**: it reclaims ~10.8 MB in one go, and clones thereafter no longer carry useless test binaries. **Why it was not done before**: it invalidates every existing clone, which is something only the repo owner can decide — and that decision has now been made.
