//go:build dev

package main

// openInspectorOnStartup gates the WebView inspector to dev builds only.
// It is compiled in when the binary is built with `-tags dev` (see
// wails.json's `build:dev` and Taskfile `pos:dev`/`pos:build:dev`).
// Release builds (`task pos:build`, no build tag) link devtools_release.go
// instead, which hardcodes this to false — the inspector cannot be enabled
// in a shipped POS station binary, per lessons-from-b2b Bölüm 5.
const openInspectorOnStartup = true
