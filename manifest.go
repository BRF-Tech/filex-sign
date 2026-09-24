// Package filexsign is the root of the filex-sign app plugin. It holds the
// manifest (filex-app.json, embedded so `describe` can never drift from the
// file the administrator installs) and nothing else; the behaviour lives in
// internal/…, the wasm entry point in cmd/plugin.
package filexsign

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

//go:embed filex-app.json
var manifestJSON []byte

// ManifestJSON is the raw filex-app.json shipped with this build.
func ManifestJSON() []byte { return manifestJSON }

// Manifest parses the embedded filex-app.json. A manifest that does not
// parse is a build error, not a runtime condition, so this panics.
func Manifest() wire.Manifest {
	m, err := ParseManifest(manifestJSON)
	if err != nil {
		panic(err)
	}
	return m
}

// ParseManifest decodes a filex-app.json strictly: unknown fields are an
// error here for the same reason filex refuses them at install.
func ParseManifest(b []byte) (wire.Manifest, error) {
	var m wire.Manifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return wire.Manifest{}, fmt.Errorf("filex-app.json: %w", err)
	}
	if m.Name == "" || m.Version == "" {
		return wire.Manifest{}, fmt.Errorf("filex-app.json: name and version are required")
	}
	if m.ManifestVersion == 0 {
		m.ManifestVersion = wire.ProtocolVersion
	}
	return m, nil
}
