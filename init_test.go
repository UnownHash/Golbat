package main

import "golbat/decoder"

// The production binary calls decoder.InitDataCache from main() after config
// load; the test binary has no main(), so construct the caches here.
func init() {
	decoder.InitDataCache()
}
