package main

import "github.com/danielgtaylor/huma/v2"

// draftBadge marks an operation as a draft API: a "Draft" badge in the Stoplight
// docs (via the x-badges extension) and a note prepended to the description. Used
// for endpoints with no stable public consumers yet.
func draftBadge(op *huma.Operation) {
	op.Description = "**Draft — subject to change.**\n\n" + op.Description
	if op.Extensions == nil {
		op.Extensions = map[string]any{}
	}
	op.Extensions["x-badges"] = []map[string]any{{"name": "Draft", "color": "orange"}}
}
