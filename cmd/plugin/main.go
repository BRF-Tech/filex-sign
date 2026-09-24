// The filex-sign wasm module. Build:
//
//	GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags="-s -w" -buildmode=c-shared -o plugin.wasm ./cmd/plugin
//
// A c-shared wasip1 module is a reactor: filex calls the exports and
// main() never runs, so registration happens in init().
package main

import (
	filexsign "github.com/brf-tech/filex-sign"
	"github.com/brf-tech/filex-sign/internal/app"
	"github.com/brf-tech/filex-sign/internal/host"
	"github.com/brf-tech/filex-sign/internal/hostnet"
	"github.com/brf-tech/filex/backend/pkg/pluginkit"
)

func main() {}

func init() {
	// digitorus/pdfsign asks a time-stamping authority with its own
	// http.Client, and a wasm guest has no sockets: route Go's default
	// transport through filex's host function, which is where the
	// permission, the address rules and the limits live.
	hostnet.Install()
	pluginkit.Run(app.New(host.Kit{}, filexsign.Manifest()).Plugin())
}
