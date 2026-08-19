package decoder

import (
	log "github.com/sirupsen/logrus"
)

// fortIdFromLegacyString parses a fort id that is still held as a string
// somewhere upstream, logging the structural failure the way any other
// unexpected data format is logged.
//
// THIS FILE IS TEMPORARY. It exists only while the fort-id conversion is
// in progress: entity Id fields become FortId in Tasks 5-7, at which point
// every call here disappears and this file is deleted. `grep -rn
// fortIdFromLegacyString` returning nothing is the completion check for the
// conversion. New callers are expected to appear while the conversion is
// mid-flight — each new FortId-typed function forces its still-string
// callers to bridge here until their own tasks land — but every call site
// is debt: the task that finally converts a given caller's id must remove
// its bridge call, and the task that removes the last one deletes this
// file. Never reach for this outside that conversion as a general-purpose
// "parse or ignore" helper.
func fortIdFromLegacyString(id string, context string) (FortId, bool) {
	f, ok := ParseFortId(id)
	if !ok {
		log.Errorf("[FORTID] %s: unparseable fort id %q, skipping", context, id)
	}
	return f, ok
}
