// Package version is the release the daemon reports. The web UI keeps the
// same number in web/src/version.ts.
package version

// Current is this build of FELDCORE. Release builds replace it with
// -X github.com/Herculade0/feldcore/internal/version.Current=<version>.
// A plain go build keeps this default. It has to be a variable: the
// linker can set -X only on a string variable, not a constant.
var Current = "0.1.2"
