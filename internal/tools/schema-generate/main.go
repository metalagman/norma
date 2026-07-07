// Command schema-generate wraps github.com/elastic/go-json-schema-generate with
// project-local compatibility fixes for current Go toolchains.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	generate "github.com/elastic/go-json-schema-generate"
)

const modeES = "es"

func main() {
	outputFile := flag.String("o", "", "The output file for the schema.")
	pkg := flag.String("p", "main", "The package that the structs are created in.")
	inputFile := flag.String("i", "", "A single file path (used for backwards compatibility).")
	schemaKeyRequired := flag.Bool("schemaKeyRequired", false, "Allow input files with no $schema key.")
	mode := flag.String("m", "", `Output mode: Default (empty) for Go structures or "es" for ES mapping`)
	conventionMapJSON := flag.String("cm", "{}", `JSON map used for field naming replacement. Ex: {"Api": "API"}`)
	skipCode := flag.Bool("s", false, "Skip marshalling code generation.")
	esdoc := flag.Bool("esdoc", false, "Generate ES Document base struct.")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr, "  paths")
		fmt.Fprintln(os.Stderr, "\tThe input JSON Schema files.")
	}
	flag.Parse()

	inputFiles := flag.Args()
	if *inputFile != "" {
		inputFiles = append(inputFiles, *inputFile)
	}
	if len(inputFiles) == 0 {
		fmt.Fprintln(os.Stderr, "No input JSON Schema files.")
		flag.Usage()
		os.Exit(1)
	}

	schemas, err := generate.ReadInputFiles(inputFiles, *schemaKeyRequired)
	if err != nil {
		fmt.Fprint(os.Stderr, err.Error())
		os.Exit(1)
	}

	generator := generate.New(schemas...)

	var conventionMap map[string]string
	if err := json.Unmarshal([]byte(*conventionMapJSON), &conventionMap); err != nil {
		fmt.Fprintln(os.Stderr, "Failure parsing convention map: ", err)
		os.Exit(1)
	}
	if err := generator.CreateTypes(conventionMap); err != nil {
		fmt.Fprintln(os.Stderr, "Failure generating structs: ", err)
		os.Exit(1)
	}

	var buf bytes.Buffer
	switch *mode {
	case modeES:
		generate.ESOutput(&buf, generator, *pkg)
	default:
		generate.Output(&buf, generator, *pkg, *skipCode, *esdoc)
	}

	output := fixGeneratedGo(buf.String())
	if *outputFile == "" {
		_, err = io.WriteString(os.Stdout, output)
	} else {
		err = os.WriteFile(*outputFile, []byte(output), 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error writing output file: ", err)
		os.Exit(1)
	}
}

func fixGeneratedGo(source string) string {
	return strings.ReplaceAll(
		source,
		`fmt.Errorf("additional property not allowed: \"" + k + "\"")`,
		`fmt.Errorf("additional property not allowed: %q", k)`,
	)
}
