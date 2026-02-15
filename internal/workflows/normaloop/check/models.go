package check

//go:generate go tool schema-generate -p check -o input.go input.schema.json
//go:generate go tool schema-generate -p check -o output.go output.schema.json
//go:generate gofmt -w input.go output.go

import _ "embed"

//go:embed input.schema.json
var InputSchema string

//go:embed output.schema.json
var OutputSchema string

//go:embed prompt.gotmpl
var PromptTemplate string
