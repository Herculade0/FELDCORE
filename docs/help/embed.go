// Package help embeds the in-app Help centre. Articles are Markdown under
// this directory. routes.json is the only map from a help button to a page.
package help

import "embed"

//go:embed routes.json
//go:embed CONTRIBUTING.md
//go:embed guides
//go:embed reference
var FS embed.FS
