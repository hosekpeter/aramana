// nolint: gochecknoglobals
package third_party

import (
	"embed"
)

//go:embed swagger-ui/*
var SwaggerFS embed.FS
