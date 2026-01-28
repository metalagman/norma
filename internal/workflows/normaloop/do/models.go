package do

//go:generate go tool schema-generate -p do -o input.go input.schema.json
//go:generate go tool schema-generate -p do -o output.go output.schema.json

import _ "embed"

//go:embed input.schema.json
var InputSchema string

//go:embed output.schema.json
var OutputSchema string

//go:embed prompt.gotmpl
var PromptTemplate string
