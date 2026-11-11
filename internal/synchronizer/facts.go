package synchronizer

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"sort"
	"strings"
)

type sourceSymbol struct {
	Name        string            `json:"name"`
	Kind        string            `json:"kind"`
	Signature   string            `json:"signature,omitempty"`
	Declaration sourceReference   `json:"declaration"`
	Evidence    []sourceReference `json:"evidence"`
}

type sourceReference struct {
	Symbol    string `json:"symbol"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Kind      string `json:"kind"`
}

type sourceFacts struct {
	Package          string            `json:"package"`
	Imports          []string          `json:"imports"`
	ExportedSymbols  []sourceSymbol    `json:"exported_symbols"`
	TestFunctions    []string          `json:"test_functions"`
	TestEvidence     []sourceSymbol    `json:"test_evidence"`
	FileEvidence     sourceReference   `json:"file_evidence"`
	ResolvedCalls    []string          `json:"resolved_calls"`
	Subtests         []sourceReference `json:"subtests"`
	TableDrivenTests []sourceReference `json:"table_driven_tests"`
}

func extractFacts(sourcePath, source string) (sourceFacts, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, sourcePath, source, parser.ParseComments)
	if err != nil {
		return sourceFacts{}, fmt.Errorf("parse source file: %w", err)
	}

	facts := sourceFacts{
		Package: file.Name.Name,
		FileEvidence: sourceReference{
			Symbol:    "file",
			StartLine: fileSet.Position(file.Package).Line,
			EndLine:   fileSet.Position(file.Package).Line,
			Kind:      "file",
		},
	}
	for _, spec := range file.Imports {
		facts.Imports = append(facts.Imports, strings.Trim(spec.Path.Value, `"`))
	}
	sort.Strings(facts.Imports)

	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			name := declaration.Name.Name
			if declaration.Recv != nil {
				name = receiverName(declaration.Recv) + "." + name
			}
			if ast.IsExported(declaration.Name.Name) && !(strings.HasSuffix(sourcePath, "_test.go") && strings.HasPrefix(declaration.Name.Name, "Test")) {
				symbol := sourceSymbol{
					Name:        name,
					Kind:        functionKind(declaration),
					Signature:   formatNode(fileSet, declaration.Type),
					Declaration: declarationReference(fileSet, name, declaration),
					Evidence:    collectFunctionEvidence(fileSet, name, declaration),
				}
				facts.ExportedSymbols = append(facts.ExportedSymbols, symbol)
			}
			if strings.HasPrefix(declaration.Name.Name, "Test") && declaration.Recv == nil {
				facts.TestFunctions = append(facts.TestFunctions, declaration.Name.Name)
				facts.TestEvidence = append(facts.TestEvidence, sourceSymbol{
					Name:        declaration.Name.Name,
					Kind:        "test",
					Declaration: declarationReference(fileSet, declaration.Name.Name, declaration),
					Evidence:    collectFunctionEvidence(fileSet, declaration.Name.Name, declaration),
				})
				facts.Subtests = append(facts.Subtests, collectTestEvidence(fileSet, declaration.Name.Name, declaration)...)
				facts.TableDrivenTests = append(facts.TableDrivenTests, collectTableEvidence(fileSet, declaration.Name.Name, declaration)...)
			}
		case *ast.GenDecl:
			for _, spec := range declaration.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					if ast.IsExported(spec.Name.Name) {
						declaration := nodeReference(fileSet, spec.Name.Name, "declaration", spec)
						facts.ExportedSymbols = append(facts.ExportedSymbols, sourceSymbol{
							Name:        spec.Name.Name,
							Kind:        "type",
							Signature:   formatNode(fileSet, spec.Type),
							Declaration: declaration,
							Evidence:    []sourceReference{declaration},
						})
					}
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						if ast.IsExported(name.Name) {
							kind := "variable"
							if declaration.Tok == token.CONST {
								kind = "constant"
							}
							reference := nodeReference(fileSet, name.Name, "declaration", spec)
							facts.ExportedSymbols = append(facts.ExportedSymbols, sourceSymbol{
								Name:        name.Name,
								Kind:        kind,
								Signature:   formatNode(fileSet, spec.Type),
								Declaration: reference,
								Evidence:    []sourceReference{reference},
							})
						}
					}
				}
			}
		}
	}
	sort.Slice(facts.ExportedSymbols, func(i, j int) bool {
		return facts.ExportedSymbols[i].Name < facts.ExportedSymbols[j].Name
	})
	sort.Strings(facts.TestFunctions)
	sort.Slice(facts.TestEvidence, func(i, j int) bool {
		return facts.TestEvidence[i].Name < facts.TestEvidence[j].Name
	})
	sort.Slice(facts.Subtests, func(i, j int) bool { return facts.Subtests[i].StartLine < facts.Subtests[j].StartLine })
	sort.Slice(facts.TableDrivenTests, func(i, j int) bool { return facts.TableDrivenTests[i].StartLine < facts.TableDrivenTests[j].StartLine })
	facts.ResolvedCalls = resolveTypeFacts(fileSet, file)
	return facts, nil
}

func collectTestEvidence(fileSet *token.FileSet, symbol string, declaration *ast.FuncDecl) []sourceReference {
	var evidence []sourceReference
	if declaration.Body == nil {
		return evidence
	}
	ast.Inspect(declaration.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Run" {
			evidence = append(evidence, nodeReference(fileSet, symbol, "subtest", call))
		}
		return true
	})
	return evidence
}

func collectTableEvidence(fileSet *token.FileSet, symbol string, declaration *ast.FuncDecl) []sourceReference {
	var evidence []sourceReference
	if declaration.Body == nil {
		return evidence
	}
	ast.Inspect(declaration.Body, func(node ast.Node) bool {
		if rangeStatement, ok := node.(*ast.RangeStmt); ok {
			evidence = append(evidence, nodeReference(fileSet, symbol, "table", rangeStatement))
		}
		return true
	})
	return evidence
}

func resolveTypeFacts(fileSet *token.FileSet, file *ast.File) []string {
	info := types.Info{
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Uses:       make(map[*ast.Ident]types.Object),
	}
	config := types.Config{Importer: importer.Default()}
	if _, err := config.Check(file.Name.Name, fileSet, []*ast.File{file}, &info); err != nil {
		return nil
	}
	resolved := make(map[string]struct{})
	for selector, selection := range info.Selections {
		if selection.Kind() != types.MethodExpr && selection.Kind() != types.MethodVal {
			continue
		}
		resolved[selection.Obj().Pkg().Path()+"."+selector.Sel.Name] = struct{}{}
	}
	for identifier, object := range info.Uses {
		function, ok := object.(*types.Func)
		if !ok || function.Pkg() == nil || function.Pkg().Path() == file.Name.Name {
			continue
		}
		resolved[function.Pkg().Path()+"."+identifier.Name] = struct{}{}
	}
	calls := make([]string, 0, len(resolved))
	for call := range resolved {
		calls = append(calls, call)
	}
	sort.Strings(calls)
	return calls
}

func declarationReference(fileSet *token.FileSet, symbol string, declaration *ast.FuncDecl) sourceReference {
	reference := nodeReference(fileSet, symbol, "declaration", declaration)
	if declaration.Doc != nil {
		reference.StartLine = fileSet.Position(declaration.Doc.Pos()).Line
	}
	return reference
}

func collectFunctionEvidence(fileSet *token.FileSet, symbol string, declaration *ast.FuncDecl) []sourceReference {
	evidence := []sourceReference{declarationReference(fileSet, symbol, declaration)}
	if declaration.Body == nil {
		return evidence
	}
	ast.Inspect(declaration.Body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.IfStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt:
			evidence = append(evidence, nodeReference(fileSet, symbol, "branch", node))
		case *ast.ReturnStmt:
			evidence = append(evidence, nodeReference(fileSet, symbol, "return", node))
		case *ast.CallExpr:
			evidence = append(evidence, nodeReference(fileSet, symbol, "call", node))
			if isAssertionCall(node) {
				evidence = append(evidence, nodeReference(fileSet, symbol, "assertion", node))
			}
		}
		return true
	})
	return evidence
}

func isAssertionCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch selector.Sel.Name {
	case "Error", "Errorf", "Fatal", "Fatalf", "Fail", "FailNow", "NoError", "Equal", "NotEqual", "True", "False":
		return true
	default:
		return false
	}
}

func nodeReference(fileSet *token.FileSet, symbol, kind string, node ast.Node) sourceReference {
	return sourceReference{
		Symbol:    symbol,
		StartLine: fileSet.Position(node.Pos()).Line,
		EndLine:   fileSet.Position(node.End()).Line,
		Kind:      kind,
	}
}

func receiverName(fields *ast.FieldList) string {
	if len(fields.List) == 0 {
		return ""
	}
	return strings.TrimPrefix(formatNode(token.NewFileSet(), fields.List[0].Type), "*")
}

func functionKind(declaration *ast.FuncDecl) string {
	if declaration.Recv != nil {
		return "method"
	}
	return "function"
}

func formatNode(fileSet *token.FileSet, node ast.Node) string {
	var buffer bytes.Buffer
	if err := format.Node(&buffer, fileSet, node); err != nil {
		return ""
	}
	return buffer.String()
}
