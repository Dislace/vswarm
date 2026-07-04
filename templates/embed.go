package templates

import "embed"

//go:embed *.tmpl njs/access-jwt.js
var FS embed.FS
