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
	namedExportedStructRoot     rootRule = "named-exported-struct"
	namedDependencyRequestRoot  rootRule = "named-dependency-request"
	namedDependencyResponseRoot rootRule = "named-dependency-response"
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

type callableTarget struct {
	function       *types.Func
	argumentOffset int
}

type aliasAssignment struct {
	variable   *types.Var
	expression ast.Expr
	info       *types.Info
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
	aliases := discoverFunctionAliases(loaded)
	wrappers := discoverWrappers(functions, aliases)
	diagnostics := rawMessageFieldDiagnostics(absoluteDirectory, loaded)
	for _, body := range functions {
		ast.Inspect(body.declaration.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			for _, sink := range callSinks(call, body.pkg.TypesInfo, wrappers, aliases) {
				if sink.argument >= len(call.Args) {
					continue
				}
				root := call.Args[sink.argument]
				if sink.rule == namedDependencyRequestRoot && isNilExpression(root) {
					continue
				}
				if forwardedParameter(body, root, sink, wrappers) {
					continue
				}
				problem := "unknown type"
				if !isNilExpression(root) {
					problem = describeInvalidRoot(body.pkg.TypesInfo.TypeOf(root), sink.rule)
				}
				if problem == "" {
					continue
				}
				position := body.pkg.Fset.Position(root.Pos())
				expectation := "must be a named exported struct"
				switch sink.rule {
				case namedExportedStructRoot:
				case namedDependencyRequestRoot:
					expectation = "must be a named struct"
				case namedDependencyResponseRoot:
					expectation = "must use a named provider DTO"
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

func discoverWrappers(functions []functionBody, aliases map[*types.Var]callableTarget) map[*types.Func][]transportSink {
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
				for _, sink := range callSinks(call, body.pkg.TypesInfo, wrappers, aliases) {
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

func callSinks(
	call *ast.CallExpr,
	info *types.Info,
	wrappers map[*types.Func][]transportSink,
	aliases map[*types.Var]callableTarget,
) []transportSink {
	target, ok := resolveCallable(call.Fun, info, aliases)
	if !ok {
		return nil
	}
	function := target.function
	var sinks []transportSink
	if function.Pkg() != nil && function.Pkg().Path() == echoPackagePath {
		switch function.Name() {
		case "Bind":
			sinks = []transportSink{{argument: 0, kind: "request", rule: namedExportedStructRoot}}
		case "JSON", "JSONPretty":
			sinks = []transportSink{{argument: 1, kind: "response", rule: namedExportedStructRoot}}
		}
	}
	if isImmichRequestMarshal(function) {
		sinks = []transportSink{{argument: 0, kind: "Immich request", rule: namedDependencyRequestRoot}}
	}
	if isImmichClientMethod(function) {
		switch function.Name() {
		case "getJSON":
			sinks = []transportSink{{argument: 2, kind: "Immich response", rule: namedDependencyResponseRoot}}
		case "getJSONQuery":
			sinks = []transportSink{{argument: 3, kind: "Immich response", rule: namedDependencyResponseRoot}}
		case "doJSON", "doJSONStatus":
			sinks = []transportSink{
				{argument: 4, kind: "Immich request", rule: namedDependencyRequestRoot},
				{argument: 5, kind: "Immich response", rule: namedDependencyResponseRoot},
			}
		}
	}
	if sinks == nil {
		sinks = wrappers[function]
	}
	return offsetSinks(sinks, target.argumentOffset)
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

func isImmichRequestMarshal(function *types.Func) bool {
	if function.Pkg() == nil || !isImmichPackage(function.Pkg().Path()) || function.Name() != "marshalJSONRequest" {
		return false
	}
	signature, _ := function.Type().(*types.Signature)
	return signature != nil && signature.Recv() == nil
}

func discoverFunctionAliases(loaded []*packages.Package) map[*types.Var]callableTarget {
	var assignments []aliasAssignment
	for _, pkg := range loaded {
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.AssignStmt:
					if len(node.Lhs) != len(node.Rhs) {
						return true
					}
					for index, left := range node.Lhs {
						identifier, ok := unparenthesized(left).(*ast.Ident)
						if !ok {
							continue
						}
						if variable := assignedVariable(identifier, pkg.TypesInfo); variable != nil {
							assignments = append(assignments, aliasAssignment{variable: variable, expression: node.Rhs[index], info: pkg.TypesInfo})
						}
					}
				case *ast.ValueSpec:
					if len(node.Names) != len(node.Values) {
						return true
					}
					for index, identifier := range node.Names {
						if variable, _ := pkg.TypesInfo.Defs[identifier].(*types.Var); variable != nil {
							assignments = append(assignments, aliasAssignment{variable: variable, expression: node.Values[index], info: pkg.TypesInfo})
						}
					}
				}
				return true
			})
		}
	}

	aliases := make(map[*types.Var]callableTarget)
	conflicts := make(map[*types.Var]bool)
	changed := true
	for changed {
		changed = false
		for _, assignment := range assignments {
			if conflicts[assignment.variable] {
				continue
			}
			target, ok := resolveCallable(assignment.expression, assignment.info, aliases)
			if !ok {
				continue
			}
			if existing, found := aliases[assignment.variable]; found && existing != target {
				delete(aliases, assignment.variable)
				conflicts[assignment.variable] = true
				changed = true
				continue
			}
			if _, found := aliases[assignment.variable]; !found {
				aliases[assignment.variable] = target
				changed = true
			}
		}
	}
	return aliases
}

func assignedVariable(identifier *ast.Ident, info *types.Info) *types.Var {
	if variable, _ := info.Defs[identifier].(*types.Var); variable != nil {
		return variable
	}
	variable, _ := info.Uses[identifier].(*types.Var)
	return variable
}

func resolveCallable(expression ast.Expr, info *types.Info, aliases map[*types.Var]callableTarget) (callableTarget, bool) {
	switch expression := unparenthesized(expression).(type) {
	case *ast.Ident:
		if function, _ := info.Uses[expression].(*types.Func); function != nil {
			return callableTarget{function: function}, true
		}
		variable, _ := info.Uses[expression].(*types.Var)
		target, ok := aliases[variable]
		return target, ok
	case *ast.SelectorExpr:
		if selection := info.Selections[expression]; selection != nil {
			function, _ := selection.Obj().(*types.Func)
			if function == nil {
				return callableTarget{}, false
			}
			offset := 0
			if selection.Kind() == types.MethodExpr {
				offset = 1
			}
			return callableTarget{function: function, argumentOffset: offset}, true
		}
		function, _ := info.Uses[expression.Sel].(*types.Func)
		return callableTarget{function: function}, function != nil
	case *ast.IndexExpr:
		return resolveCallable(expression.X, info, aliases)
	case *ast.IndexListExpr:
		return resolveCallable(expression.X, info, aliases)
	default:
		return callableTarget{}, false
	}
}

func offsetSinks(sinks []transportSink, offset int) []transportSink {
	if offset == 0 || len(sinks) == 0 {
		return sinks
	}
	result := make([]transportSink, len(sinks))
	for index, sink := range sinks {
		sink.argument += offset
		result[index] = sink
	}
	return result
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
	root = dereference(root)
	if rule == namedDependencyRequestRoot {
		return describeInvalidDependencyRequestRoot(root)
	}
	if rule == namedDependencyResponseRoot {
		return describeInvalidDependencyResponseRoot(root)
	}
	if named, ok := root.(*types.Named); ok {
		underlying := named.Underlying()
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
	return typeCategory(root.Underlying())
}

func dereference(root types.Type) types.Type {
	root = types.Unalias(root)
	for {
		pointer, ok := root.(*types.Pointer)
		if !ok {
			return root
		}
		root = types.Unalias(pointer.Elem())
	}
}

func describeInvalidDependencyRequestRoot(root types.Type) string {
	named, ok := root.(*types.Named)
	if !ok {
		return typeCategory(root.Underlying())
	}
	if _, ok := named.Underlying().(*types.Struct); !ok {
		return typeCategory(named.Underlying())
	}
	if named.Obj().Pkg() == nil || !isImmichPackage(named.Obj().Pkg().Path()) {
		return "external struct " + named.Obj().Name()
	}
	return ""
}

func describeInvalidDependencyResponseRoot(root types.Type) string {
	if named, ok := root.(*types.Named); ok {
		switch named.Underlying().(type) {
		case *types.Map:
			return "map"
		case *types.Interface:
			return "interface"
		default:
			return ""
		}
	}
	switch root := root.Underlying().(type) {
	case *types.Slice:
		return describeInvalidDependencyResponseRoot(dereference(root.Elem()))
	case *types.Array:
		return describeInvalidDependencyResponseRoot(dereference(root.Elem()))
	default:
		return typeCategory(root)
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
	case *types.TypeParam:
		return "unresolved type parameter"
	default:
		return "unnamed type"
	}
}

func isNilExpression(expression ast.Expr) bool {
	identifier, ok := unparenthesized(expression).(*ast.Ident)
	return ok && identifier.Name == "nil"
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
