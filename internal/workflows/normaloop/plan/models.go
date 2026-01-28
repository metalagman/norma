package plan

import _ "embed"

//go:generate go tool schema-generate -p plan -o input.go input.schema.json
//go:generate go tool schema-generate -p plan -o output.go output.schema.json

//go:embed input.schema.json
var InputSchema string

//go:embed output.schema.json
var OutputSchema string

//go:embed prompt.gotmpl
var PromptTemplate string
