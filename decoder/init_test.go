package decoder

// The production binary calls InitDataCache from main() after config load;
// the test binary has no main(), so construct the caches here.
func init() {
	InitDataCache()
}
