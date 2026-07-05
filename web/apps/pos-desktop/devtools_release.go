//go:build !dev

package main

// openInspectorOnStartup is hardcoded false for every build that does not
// pass `-tags dev`. This is the default — `wails build` and `task
// pos:build` never include the dev tag — so a station binary shipped to
// production can never have the WebView inspector auto-opened, regardless
// of how the build is invoked. See devtools_dev.go for the dev-only
// counterpart.
const openInspectorOnStartup = false
