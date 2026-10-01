package pogo

import _ "embed"

// The bundled copies of the two files golbat otherwise fetches: the
// masterfile, and the published Ditto disguise pool. Each is the last
// fallback when neither its URL nor its cache can be read. They are embedded
// so they are there wherever the binary runs — a container image built from
// the binary alone has no pogo/ directory beside it.

//go:embed master-latest-golbat.json
var BundledMasterFile []byte

//go:embed ditto-pool.json
var BundledDittoPool []byte
