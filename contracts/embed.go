// Package contracts embeds the reviewed HTTP contract for the standalone documentation command.
package contracts

import _ "embed"

// OpenAPI is the portable contract served by cmd/openapi.
//
//go:embed openapi.json
var OpenAPI []byte
