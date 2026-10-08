// Hand-written definitions for stable methods whose payload types upstream does not carry
// in the v2 aggregate schema.
//
// The vendored aggregate is exactly what upstream's schema export produces; it is not a
// complete picture of the protocol. A handful of types a *stable* method needs live
// elsewhere in the crate (under `protocol/` instead of `protocol/v2/`, or declared in
// `common.rs` directly), so they are modelled here from source.
//
// Every entry must state its origin and why it is absent, and must be listed in
// gen/type-allowlist.json -- otherwise `make typecheck` fails, which is the point: this
// file is the one place where "we invented a type" is allowed, and it has to justify itself.

package schema

// FuzzyFileSearchResponse is the reply to the stable `fuzzyFileSearch` request.
//
// Origin: app-server-protocol/src/protocol/common.rs:1882-1885. It is declared in
// `common.rs` rather than under `protocol/v2/`, which is why the v2 aggregate omits it
// while still carrying FuzzyFileSearchParams/FuzzyFileSearchResult/FuzzyFileSearchMatchType.
//
// Upstream applies no `rename_all` here, so there is nothing to translate.
type FuzzyFileSearchResponse struct {
	Files []FuzzyFileSearchResult `json:"files"`
}
