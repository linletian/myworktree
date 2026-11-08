//go:build !viewportdebug

package ui

// ViewportDebugEnabled is false for stable builds. viewportDebugAsset stays
// nil and /static/viewport_debug.js returns 404 in Register.
const ViewportDebugEnabled = false

var viewportDebugAsset []byte
