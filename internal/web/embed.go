// Package web embeds the built-in frontend assets.
package web

import "embed"

//go:embed static
var Static embed.FS
