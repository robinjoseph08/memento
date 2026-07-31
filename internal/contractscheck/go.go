package contractscheck

import (
	"errors"
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

const echoPackagePath = "github.com/labstack/echo/v4"

var errPackageLoading = errors.New("go package loading failed")

type rootRule string

const (
	namedExportedStructRoot rootRule = "named-exported-struct"
	namedDependencyRoot     rootRule = "named-dependency"
)

type transportSink struct {
	argument int
	kind     string
	rule     rootRule
}

type functionBody struct {
	declaration *ast.FuncDecl
	function    *types.Func
	pkg         *packages.Package
}

// CheckGo finds JSON contract roots crossing production HTTP and dependency seams.
func CheckGo(directory string, patterns ...string) ([]string, error) {
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve Go check directory: %w", err)
	}
	loaded, err := packages.Load(&packages.Config{
		Dir: absoluteDirectory,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedImports | packages.NeedDeps,
		Tests: false,
	}, patterns...)
	if err != nil {
		return nil, fmt.Errorf("load Go packages: %w", err)
	}
	if loadErr := packageErrors(loaded); loadErr != nil {
		return nil, loadErr
	}

	functions := collectFunctionBodies(loaded)
	wrappers := discoverWrappers(functions)
	diagnostics := rawMessageFieldDiagnostics(absoluteDirectory, loaded)
	for _, body := range functions {
		ast.Inspect(body.declaration.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			for _, sink := range callSinks(call, body.pkg.TypesInfo, body.pkg.PkgPath, wrappers) {
				if sink.argument >= len(call.Args) {
					continue
				}
				root := call.Args[sink.argument]
				if forwardedParameter(body, root, sink, wrappers) {
					continue
				}
				problem := describeInvalidRoot(body.pkg.TypesInfo.TypeOf(root), sink.rule)
				if problem == "" {
					continue
				}
				position := body.pkg.Fset.Position(root.Pos())
				expectation := "must be a named exported struct"
				if sink.rule == namedDependencyRoot {
					expectation = "must not use an anonymous struct or map"
				}
				diagnostics = append(diagnostics, fmt.Sprintf(
					"%s:%d:%d: %s JSON contract %s; got %s",
					relativePath(absoluteDirectory, position.Filename), position.Line, position.Column,
					sink.kind, expectation, problem,
				))
			}
			return true
		})
	}
	slices.Sort(diagnostics)
	return diagnostics, nil
}

func packageErrors(loaded []*packages.Package) error {
	var messages []string
	for _, pkg := range loaded {
		for _, loadErr := range pkg.Errors {
			messages = append(messages, loadErr.Error())
		}
	}
	if len(messages) == 0 {
		return nil
	}
	slices.Sort(messages)
	return fmt.Errorf("%w:\n%s", errPackageLoading, strings.Join(messages, "\n"))
}

func collectFunctionBodies(loaded []*packages.Package) []functionBody {
	var result []functionBody
	for _, pkg := range loaded {
		for _, file := range pkg.Syntax {
			filename := pkg.Fset.Position(file.Pos()).Filename
			if strings.HasSuffix(filename, "_test.go") {
				continue
			}
			for _, declaration := range file.Decls {
				functionDeclaration, ok := declaration.(*ast.FuncDecl)
				if !ok || functionDeclaration.Body == nil {
					continue
				}
				function, _ := pkg.TypesInfo.Defs[functionDeclaration.Name].(*types.Func)
				if function == nil {
					continue
				}
				result = append(result, functionBody{
					declaration: functionDeclaration,
					function:    function,
					pkg:         pkg,
				})
			}
		}
	}
	return result
}

func discoverWrappers(functions []functionBody) map[*types.Func][]transportSink {
	wrappers := make(map[*types.Func][]transportSink)
	changed := true
	for changed {
		changed = false
		for _, body := range functions {
			ast.Inspect(body.declaration.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				for _, sink := range callSinks(call, body.pkg.TypesInfo, body.pkg.PkgPath, wrappers) {
					if sink.argument >= len(call.Args) {
						continue
					}
					parameter := parameterIndex(body.function, body.pkg.TypesInfo, call.Args[sink.argument])
					if parameter < 0 {
						continue
					}
					forwarded := transportSink{argument: parameter, kind: sink.kind, rule: sink.rule}
					if !slices.Contains(wrappers[body.function], forwarded) {
						wrappers[body.function] = append(wrappers[body.function], forwarded)
						changed = true
					}
				}
				return true
			})
		}
	}
	return wrappers
}

func callSinks(call *ast.CallExpr, info *types.Info, packagePath string, wrappers map[*types.Func][]transportSink) []transportSink {
	function := calledFunction(call.Fun, info)
	if function == nil {
		return nil
	}
	if function.Pkg() != nil && function.Pkg().Path() == echoPackagePath {
		switch function.Name() {
		case "Bind":
			return []transportSink{{argument: 0, kind: "request", rule: namedExportedStructRoot}}
		case "JSON", "JSONPretty":
			return []transportSink{{argument: 1, kind: "response", rule: namedExportedStructRoot}}
		}
	}
	if isImmichPackage(packagePath) && function.Pkg() != nil && function.Pkg().Path() == "encoding/json" && function.Name() == "Marshal" {
		return []transportSink{{argument: 0, kind: "Immich request", rule: namedDependencyRoot}}
	}
	if isImmichClientMethod(function) {
		switch function.Name() {
		case "getJSON":
			return []transportSink{{argument: 2, kind: "Immich response", rule: namedDependencyRoot}}
		case "getJSONQuery":
			return []transportSink{{argument: 3, kind: "Immich response", rule: namedDependencyRoot}}
		case "doJSON", "doJSONStatus":
			return []transportSink{{argument: 5, kind: "Immich response", rule: namedDependencyRoot}}
		}
	}
	return wrappers[function]
}

func isImmichPackage(packagePath string) bool {
	return strings.HasSuffix(packagePath, "/pkg/immich")
}

func isImmichClientMethod(function *types.Func) bool {
	if function.Pkg() == nil || !isImmichPackage(function.Pkg().Path()) {
		return false
	}
	signature, _ := function.Type().(*types.Signature)
	if signature == nil || signature.Recv() == nil {
		return false
	}
	receiver := types.Unalias(signature.Recv().Type())
	if pointer, ok := receiver.(*types.Pointer); ok {
		receiver = types.Unalias(pointer.Elem())
	}
	named, ok := receiver.(*types.Named)
	return ok && named.Obj().Name() == "Client"
}

func calledFunction(expression ast.Expr, info *types.Info) *types.Func {
	switch expression := unparenthesized(expression).(type) {
	case *ast.Ident:
		function, _ := info.Uses[expression].(*types.Func)
		return function
	case *ast.SelectorExpr:
		if selection := info.Selections[expression]; selection != nil {
			function, _ := selection.Obj().(*types.Func)
			return function
		}
		function, _ := info.Uses[expression.Sel].(*types.Func)
		return function
	case *ast.IndexExpr:
		return calledFunction(expression.X, info)
	case *ast.IndexListExpr:
		return calledFunction(expression.X, info)
	default:
		return nil
	}
}

func forwardedParameter(body functionBody, root ast.Expr, sink transportSink, wrappers map[*types.Func][]transportSink) bool {
	parameter := parameterIndex(body.function, body.pkg.TypesInfo, root)
	return parameter >= 0 && slices.Contains(wrappers[body.function], transportSink{argument: parameter, kind: sink.kind, rule: sink.rule})
}

func parameterIndex(function *types.Func, info *types.Info, expression ast.Expr) int {
	identifier, ok := unparenthesized(expression).(*ast.Ident)
	if !ok {
		return -1
	}
	variable, _ := info.Uses[identifier].(*types.Var)
	if variable == nil {
		return -1
	}
	signature, _ := function.Type().(*types.Signature)
	if signature == nil {
		return -1
	}
	for index := range signature.Params().Len() {
		if signature.Params().At(index) == variable {
			return index
		}
	}
	return -1
}

func describeInvalidRoot(root types.Type, rule rootRule) string {
	if root == nil {
		return "unknown type"
	}
	root = types.Unalias(root)
	for {
		pointer, ok := root.(*types.Pointer)
		if !ok {
			break
		}
		root = types.Unalias(pointer.Elem())
	}
	if named, ok := root.(*types.Named); ok {
		underlying := named.Underlying()
		if rule == namedDependencyRoot {
			return describeInvalidDependencyRoot(underlying, true)
		}
		if _, ok := underlying.(*types.Struct); ok && named.Obj().Exported() {
			return ""
		}
		if !named.Obj().Exported() {
			category := typeCategory(underlying)
			if _, ok := underlying.(*types.Struct); ok {
				category = "struct"
			}
			return "unexported " + category + " " + named.Obj().Name()
		}
		return typeCategory(underlying)
	}
	if rule == namedDependencyRoot {
		return describeInvalidDependencyRoot(root.Underlying(), false)
	}
	return typeCategory(root.Underlying())
}

func describeInvalidDependencyRoot(root types.Type, named bool) string {
	switch root := root.(type) {
	case *types.Struct:
		if named {
			return ""
		}
		return "anonymous struct"
	case *types.Map:
		return "map"
	case *types.Slice:
		return describeInvalidRoot(root.Elem(), namedDependencyRoot)
	case *types.Array:
		return describeInvalidRoot(root.Elem(), namedDependencyRoot)
	default:
		return ""
	}
}

func typeCategory(root types.Type) string {
	switch root.(type) {
	case *types.Struct:
		return "anonymous struct"
	case *types.Map:
		return "map"
	case *types.Slice:
		return "slice"
	case *types.Array:
		return "array"
	case *types.Interface:
		return "interface"
	case *types.Basic:
		return "scalar"
	default:
		return "unnamed type"
	}
}

func unparenthesized(expression ast.Expr) ast.Expr {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			return expression
		}
		expression = parenthesized.X
	}
}

func rawMessageFieldDiagnostics(root string, loaded []*packages.Package) []string {
	var diagnostics []string
	for _, pkg := range loaded {
		if !isImmichPackage(pkg.PkgPath) {
			continue
		}
		for _, file := range pkg.Syntax {
			filename := pkg.Fset.Position(file.Pos()).Filename
			if strings.HasSuffix(filename, "_test.go") {
				continue
			}
			ast.Inspect(file, func(node ast.Node) bool {
				typeSpec, ok := node.(*ast.TypeSpec)
				if !ok {
					return true
				}
				structure, ok := typeSpec.Type.(*ast.StructType)
				if !ok || !hasJSONFields(structure) {
					return false
				}
				for _, field := range structure.Fields.List {
					if !containsJSONRawMessage(pkg.TypesInfo.TypeOf(field.Type)) {
						continue
					}
					position := pkg.Fset.Position(field.Type.Pos())
					fieldName := "embedded"
					if len(field.Names) > 0 {
						fieldName = field.Names[0].Name
					}
					diagnostics = append(diagnostics, fmt.Sprintf(
						"%s:%d:%d: Immich provider DTO %s.%s must not contain json.RawMessage fields",
						relativePath(root, position.Filename), position.Line, position.Column, typeSpec.Name.Name, fieldName,
					))
				}
				return false
			})
		}
	}
	return diagnostics
}

func hasJSONFields(structure *ast.StructType) bool {
	for _, field := range structure.Fields.List {
		if field.Tag != nil && strings.Contains(field.Tag.Value, "json:") {
			return true
		}
	}
	return false
}

func containsJSONRawMessage(root types.Type) bool {
	root = types.Unalias(root)
	if named, ok := root.(*types.Named); ok {
		return named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "encoding/json" && named.Obj().Name() == "RawMessage"
	}
	switch root := root.(type) {
	case *types.Pointer:
		return containsJSONRawMessage(root.Elem())
	case *types.Slice:
		return containsJSONRawMessage(root.Elem())
	case *types.Array:
		return containsJSONRawMessage(root.Elem())
	case *types.Map:
		return containsJSONRawMessage(root.Key()) || containsJSONRawMessage(root.Elem())
	default:
		return false
	}
}

func relativePath(root, filename string) string {
	relative, err := filepath.Rel(root, filename)
	if err != nil {
		return filepath.ToSlash(filename)
	}
	return filepath.ToSlash(relative)
}
