package main

import (
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func TestDraftBadge(t *testing.T) {
	op := &huma.Operation{Description: "Does a thing."}
	draftBadge(op)
	if op.Extensions["x-badges"] == nil {
		t.Errorf("expected x-badges to be set")
	}
	if !strings.HasPrefix(op.Description, "**Draft") {
		t.Errorf("expected description to start with the Draft note, got %q", op.Description)
	}
}
