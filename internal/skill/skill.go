// Package skill embeds the muveectl skill markdown so it can be shared
// between the CLI binary and the server API.
package skill

import _ "embed"

//go:embed skill.md
var Muveectl string
