// Package notofixture gives tests the pinned Noto faces: from a cache in the
// temporary directory, downloaded the first time through the same pin the
// app uses — a file whose sha256 differs from its pin is never returned.
//
// Tests only. The app itself gets its faces from the host (asset_fetch),
// never over its own connection; this package exists so the tests of
// several packages (fontkit, pdfdoc) can draw real Arabic, Devanagari or
// Japanese without each keeping its own downloader.
package notofixture

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

var (
	mu  sync.Mutex
	dir = filepath.Join(os.TempDir(), "filex-sign-noto")
)

// Required reports whether the environment says the fonts MUST be had
// (FILEX_SIGN_REQUIRE_FONTS): a test that cannot reach them then fails
// instead of skipping, so a CI run with a network cannot pass by skipping.
func Required() bool { return os.Getenv("FILEX_SIGN_REQUIRE_FONTS") != "" }

// Fetch returns the bytes pinned by sha, from the cache or from url.
func Fetch(url, sha string) ([]byte, error) {
	mu.Lock()
	defer mu.Unlock()
	p := filepath.Join(dir, sha+".ttf")
	if b, err := os.ReadFile(p); err == nil && fmt.Sprintf("%x", sha256.Sum256(b)) == sha {
		return b, nil
	}
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if fmt.Sprintf("%x", sha256.Sum256(b)) != sha {
		return nil, errors.New("integrity: " + url + " does not match its pin")
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(p, b, 0o644)
	return b, nil
}
