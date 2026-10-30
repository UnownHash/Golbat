package decoder

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

// remoteFile is a JSON document golbat fetches from a URL, caches on disk,
// refreshes on a timer, and falls back to a copy embedded in the binary when
// neither the URL nor the cache can be read. The masterfile and the Ditto
// disguise pool are both one.
//
// apply parses a candidate document and, only if it is acceptable, installs
// it. An error leaves what is installed in place, which is what makes every
// source below safe to try: nothing half-parsed is ever in force.
type remoteFile struct {
	name      string // for log lines
	url       string
	cachePath string
	bundled   []byte
	client    *http.Client
	apply     func([]byte) error
	// onRefresh runs after the watcher installs a changed document.
	onRefresh func()

	mu       sync.Mutex
	raw      []byte // the document in force
	watching atomic.Bool
}

// A remote document is small; one that is not is not the document.
const remoteFileMaxBytes = 16 << 20

// ensure installs the document from the URL, else the cache, else the
// bundled copy, caching a successful fetch. It returns which source was used,
// or an error when none of them could be applied.
func (r *remoteFile) ensure() (source string, err error) {
	data, fetchErr := r.download()
	if fetchErr == nil {
		if fetchErr = r.install(data); fetchErr == nil {
			r.saveCache(data)
			return r.url, nil
		}
	}
	log.Warnf("%s: fetching %s failed: %v", r.name, r.url, fetchErr)

	data, cacheErr := os.ReadFile(r.cachePath)
	if cacheErr == nil {
		if cacheErr = r.install(data); cacheErr == nil {
			return r.cachePath, nil
		}
	}
	log.Warnf("%s: loading the cache %s failed: %v", r.name, r.cachePath, cacheErr)

	if bundledErr := r.install(r.bundled); bundledErr != nil {
		return "", fmt.Errorf("%s unavailable (fetch: %v, cache: %v, bundled: %w)", r.name, fetchErr, cacheErr, bundledErr)
	}
	return "the copy bundled in the binary", nil
}

// refresh fetches the document once and installs it if it changed. A failed
// fetch or a document apply refuses keeps what is in force.
func (r *remoteFile) refresh() {
	data, err := r.download()
	if err != nil {
		log.Warnf("%s: refreshing from %s failed, keeping the current one: %v", r.name, r.url, err)
		return
	}
	r.mu.Lock()
	same := bytes.Equal(r.raw, data)
	r.mu.Unlock()
	if same {
		return
	}
	if err := r.install(data); err != nil {
		log.Warnf("%s: the document at %s was refused, keeping the current one: %v", r.name, r.url, err)
		return
	}
	r.saveCache(data)
	if r.onRefresh != nil {
		r.onRefresh()
	}
}

// watch refreshes every interval. Starting it twice is an error rather than
// two tickers.
func (r *remoteFile) watch(interval time.Duration) error {
	if !r.watching.CompareAndSwap(false, true) {
		return fmt.Errorf("%s watcher is already running", r.name)
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			r.refresh()
		}
	}()
	return nil
}

func (r *remoteFile) install(data []byte) error {
	if len(data) == 0 {
		return errors.New("empty document")
	}
	if err := r.apply(data); err != nil {
		return err
	}
	r.mu.Lock()
	r.raw = data
	r.mu.Unlock()
	return nil
}

// current returns the document in force, or nil before one is.
func (r *remoteFile) current() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.raw
}

func (r *remoteFile) download() ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, r.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Golbat")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, remoteFileMaxBytes))
}

// saveCache writes the document beside its final name and renames it into
// place, so a crash or a full disk mid-write leaves the previous cache rather
// than a truncated one — a truncated cache is unreadable at the next start,
// exactly when the URL may also be down.
func (r *remoteFile) saveCache(data []byte) {
	if err := writeFileAtomic(r.cachePath, data); err != nil {
		log.Warnf("%s: storing the cache under %s failed: %v", r.name, r.cachePath, err)
	}
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	// Removing it is a no-op once renamed, and after a failure there is
	// nothing to do about a temp file that will not go.
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() // the write error is the one worth reporting
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
