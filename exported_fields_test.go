package codexgo_test

import (
	"encoding/json"
	"testing"

	codexgo "github.com/zealbase/codex-app-server-go"
)

// A field of an exported struct is useless if its TYPE cannot be named from outside the module.
// The generated types live in internal/protocol/schema, so anything that appears in an exported
// struct's field must be re-exported alongside it -- otherwise a caller can see the field and
// never set or read it.
//
// This file is in the external test package on purpose: if it compiles, an external caller can
// name these types. It is the proof, not a description.
//
// The regression this pins: ThreadListCwdFilter and ForcedChatgptWorkspaceIds were
// `= json.RawMessage`, so `ThreadListParams.CWD` and `Config.ForcedChatgptWorkspaceIds` were
// settable from outside. Generating them as structs in the internal package silently made those
// two fields unsettable, with nothing failing.

func TestExportedStructFieldsAreNameable(t *testing.T) {
	// ThreadListParams.CWD accepts a string or a list of working directories.
	stringFilter := codexgo.ThreadListCwdFilterFromString("/repo")
	listFilter := codexgo.ThreadListCwdFilterFromList([]string{"/a", "/b"})

	params := codexgo.ThreadListParams{
		Limit:             5,
		CWD:               &stringFilter,
		ExcludedThreadIDs: []string{"thr-1"},
	}
	if params.CWD == nil || params.CWD.String == nil || *params.CWD.String != "/repo" {
		t.Fatalf("string arm not usable: %+v", params.CWD)
	}

	encoded, err := json.Marshal(listFilter)
	if err != nil {
		t.Fatalf("marshal list arm: %v", err)
	}
	if string(encoded) != `["/a","/b"]` {
		t.Fatalf("list arm encoded as %s", encoded)
	}

	// Config.ForcedChatgptWorkspaceIds is the same shape.
	forced := codexgo.ForcedChatgptWorkspaceIdsFromList([]string{"ws-1"})
	if forced.List == nil || forced.List[0] != "ws-1" {
		t.Fatalf("forced workspace ids not usable: %+v", forced)
	}

	// The remaining exported fields are plain structs or enums, which need only the alias.
	var edit codexgo.ConfigEdit
	edit.KeyPath = "model"
	if edit.KeyPath != "model" {
		t.Fatalf("ConfigEdit not usable: %+v", edit)
	}
	var layer codexgo.ConfigLayer
	_ = layer
	var kind codexgo.ThreadSourceKind = codexgo.ThreadSourceKindSubAgentThreadSpawn
	if kind != "subAgentThreadSpawn" {
		t.Fatalf("ThreadSourceKind = %q", kind)
	}
	var item codexgo.ThreadItem
	item.Type = codexgo.ItemKindContextCompaction
	if item.Type != "contextCompaction" {
		t.Fatalf("ThreadItem not usable: %+v", item)
	}
}

// The same rule, applied to the *Params fields a caller must POPULATE. These six were found by
// scripts/export_check.py after the two above were fixed -- they had been unusable since the
// params types were first generated, and nothing failed because a field whose type is merely
// unnamed still compiles; it just cannot be set.
func TestExportedParamsFieldTypesAreNameable(t *testing.T) {
	// thread/list: sorting.
	direction := codexgo.SortDirectionDesc
	sortKey := codexgo.ThreadSortKeyUpdatedAt
	listParams := codexgo.ThreadListParams{
		SortDirection: &direction,
		SortKey:       &sortKey,
	}
	if listParams.SortDirection == nil || *listParams.SortDirection != "desc" {
		t.Fatalf("SortDirection unusable: %+v", listParams.SortDirection)
	}
	if listParams.SortKey == nil || *listParams.SortKey != "updated_at" {
		t.Fatalf("ThreadSortKey unusable: %+v", listParams.SortKey)
	}

	// thread/fork: which threads to fork from.
	source := codexgo.ThreadSource("subAgentThreadSpawn")
	forkParams := codexgo.ThreadForkParams{ThreadSource: &source}
	if forkParams.ThreadSource == nil {
		t.Fatal("ThreadSource unusable")
	}

	// mcp/resourceRead: the target is a struct the caller builds.
	var target codexgo.McpResourceReadTarget
	readParams := codexgo.McpResourceReadParams{Target: &target}
	if readParams.Target == nil {
		t.Fatal("McpResourceReadTarget unusable")
	}

	// plugin/list and plugin/share/save: enums used as inputs.
	listing := codexgo.PluginListParams{
		MarketplaceKinds: []codexgo.PluginListMarketplaceKind{
			codexgo.PluginListMarketplaceKindLocal,
			codexgo.PluginListMarketplaceKindWorkspaceDirectory,
		},
	}
	if len(listing.MarketplaceKinds) != 2 {
		t.Fatalf("PluginListMarketplaceKind unusable: %+v", listing.MarketplaceKinds)
	}
	discoverability := codexgo.PluginShareDiscoverabilityUNLISTED
	shareParams := codexgo.PluginShareSaveParams{Discoverability: &discoverability}
	if shareParams.Discoverability == nil || *shareParams.Discoverability != "UNLISTED" {
		t.Fatalf("PluginShareDiscoverability unusable: %+v", shareParams.Discoverability)
	}
}
