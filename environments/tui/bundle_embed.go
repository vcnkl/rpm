//go:build tui_bundle

package envtui

import _ "embed"

//go:embed dist/index.js
var embeddedBundle []byte
