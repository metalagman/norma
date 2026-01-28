package check

import _ "embed"

//go:embed input.schema.json
var InputSchema string

//go:embed output.schema.json
var OutputSchema string

//go:embed prompt.gotmpl
var PromptTemplate string
