package decoder

import (
	"testing"

	"github.com/guregu/null/v6"
)

// A pokemon whose expiry becomes verified must emit a webhook: consumers read
// disappear_time/disappear_time_verified from the payload and otherwise keep
// the stale provisional value.
func TestWebhookNeededOnExpireVerifiedTransition(t *testing.T) {
	p := &Pokemon{PokemonData: PokemonData{ExpireTimestampVerified: false}}
	p.snapshotOldValues()

	if p.webhookNeeded() {
		t.Fatal("no change must not need a webhook")
	}

	p.SetExpireTimestampVerified(true)
	if !p.webhookNeeded() {
		t.Fatal("unverified -> verified must need a webhook")
	}
}

// The reverse transition matters too: a contradicted despawn drops back to
// unverified and consumers must be told.
func TestWebhookNeededOnExpireUnverifiedTransition(t *testing.T) {
	p := &Pokemon{PokemonData: PokemonData{ExpireTimestampVerified: true}}
	p.snapshotOldValues()

	p.SetExpireTimestampVerified(false)
	if !p.webhookNeeded() {
		t.Fatal("verified -> unverified must need a webhook")
	}
}

// The existing triggers must keep working.
func TestWebhookNeededOnCpChange(t *testing.T) {
	p := &Pokemon{PokemonData: PokemonData{}}
	p.snapshotOldValues()

	p.SetCp(null.IntFrom(500))
	if !p.webhookNeeded() {
		t.Fatal("cp change must still need a webhook")
	}
}
