//go:build viewportdebug

package ui

import _ "embed"

// viewportDebugAsset is embedded only in viewportdebug builds. Stable builds
// have no diagnostic script bytes in their binary at all.
//
//go:embed debugassets/viewport_debug.js
var viewportDebugAsset []byte

const ViewportDebugEnabled = true
