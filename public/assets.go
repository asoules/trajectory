package assets

import "embed"

//go:embed *.html *.css *.js *.svg
var Files embed.FS
