package synchronizer

import (
	"strings"
	"testing"
)

func TestExtractFactsFindsExportedSymbolsImportsAndTests(t *testing.T) {
	t.Parallel()

	facts, err := extractFacts("internal/demo/example_test.go", `package demo

import (
	"fmt"
	"example.com/project/internal/helper"
)

// Client serves requests.
type Client struct{}

// NewClient creates a client.
func NewClient(name string) *Client { return &Client{} }

func TestNewClient(t *testing.T) {
	fmt.Println(helper.Name)
}
`)
	if err != nil {
		t.Fatalf("extractFacts() error = %v", err)
	}
	if got, want := strings.Join(facts.Imports, ","), "example.com/project/internal/helper,fmt"; got != want {
		t.Fatalf("extractFacts() imports = %q, want %q", got, want)
	}
	if got, want := strings.Join(symbolNames(facts.ExportedSymbols), ","), "Client,NewClient"; got != want {
		t.Fatalf("extractFacts() exports = %q, want %q", got, want)
	}
	if got, want := strings.Join(facts.TestFunctions, ","), "TestNewClient"; got != want {
		t.Fatalf("extractFacts() tests = %q, want %q", got, want)
	}
	if got := facts.ExportedSymbols[0].Declaration.StartLine; got != 9 {
		t.Fatalf("extractFacts() Client declaration line = %d, want 9", got)
	}
	if len(facts.TestEvidence) != 1 || len(facts.TestEvidence[0].Evidence) == 0 {
		t.Fatalf("extractFacts() test evidence = %#v", facts.TestEvidence)
	}
}

func TestExtractFactsResolvesStandardLibraryCalls(t *testing.T) {
	t.Parallel()

	facts, err := extractFacts("normal.go", `package demo

import "strings"

func Normalize(value string) string {
	return strings.TrimSpace(value)
}
`)
	if err != nil {
		t.Fatalf("extractFacts() error = %v", err)
	}
	if got, want := strings.Join(facts.ResolvedCalls, ","), "strings.TrimSpace"; got != want {
		t.Fatalf("extractFacts() resolved calls = %q, want %q", got, want)
	}
}

func TestExtractFactsIncludesDeclarationEvidenceForTypesAndValues(t *testing.T) {
	t.Parallel()

	facts, err := extractFacts("normal.go", `package demo

type Client struct{}
const Limit = 1
var Name string
`)
	if err != nil {
		t.Fatalf("extractFacts() error = %v", err)
	}
	for _, symbol := range facts.ExportedSymbols {
		if len(symbol.Evidence) != 1 || symbol.Evidence[0] != symbol.Declaration {
			t.Fatalf("extractFacts() evidence for %q = %#v, want declaration", symbol.Name, symbol.Evidence)
		}
	}
}

func TestExtractFactsFindsSubtestsAndTableTests(t *testing.T) {
	t.Parallel()

	facts, err := extractFacts("normal_test.go", `package demo

import "testing"

func TestTable(t *testing.T) {
	for _, testCase := range []struct{ name string }{{name: "case"}} {
		t.Run(testCase.name, func(t *testing.T) {})
	}
}
`)
	if err != nil {
		t.Fatalf("extractFacts() error = %v", err)
	}
	if len(facts.Subtests) != 1 || facts.Subtests[0].Kind != "subtest" {
		t.Fatalf("extractFacts() subtests = %#v", facts.Subtests)
	}
	if len(facts.TableDrivenTests) != 1 || facts.TableDrivenTests[0].Kind != "table" {
		t.Fatalf("extractFacts() table tests = %#v", facts.TableDrivenTests)
	}
}

func symbolNames(symbols []sourceSymbol) []string {
	names := make([]string, len(symbols))
	for index, symbol := range symbols {
		names[index] = symbol.Name
	}
	return names
}
