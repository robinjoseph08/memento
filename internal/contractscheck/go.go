package contractscheck

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

const echoPackagePath = "github.com/labstack/echo/v4"

var errPackageLoading = errors.New("go package loading failed")

type rootRule string

const (
	namedExportedRequestRoot    rootRule = "named-exported-request"
	namedExportedResponseRoot   rootRule = "named-exported-response"
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
	receiver       types.Type
	argumentOffset int
}

type enforcedRoot struct {
	typeOf types.Type
	rule   rootRule
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
	var diagnostics []string
	var enforcedRoots []enforcedRoot
	for _, body := range functions {
		ast.Inspect(body.declaration.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			for _, sink := range callSinks(call, body.pkg.TypesInfo, wrappers) {
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
				rootType := body.pkg.TypesInfo.TypeOf(root)
				problem := "unknown type"
				if !isNilExpression(root) {
					problem = describeInvalidRoot(body.pkg.TypesInfo.TypeOf(root), sink.rule)
				}
				if problem == "" {
					enforcedRoots = append(enforcedRoots, enforcedRoot{typeOf: rootType, rule: sink.rule})
					continue
				}
				position := body.pkg.Fset.Position(root.Pos())
				expectation := "must be a named exported struct"
				switch sink.rule {
				case namedExportedRequestRoot, namedExportedResponseRoot:
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
	diagnostics = append(diagnostics, protectedFunctionValueDiagnostics(absoluteDirectory, loaded, wrappers)...)
	diagnostics = append(diagnostics, contractGraphDiagnostics(absoluteDirectory, loaded, enforcedRoots)...)
	diagnostics = append(diagnostics, rawMessageFieldDiagnostics(absoluteDirectory, loaded, enforcedRoots)...)
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
				for _, sink := range callSinks(call, body.pkg.TypesInfo, wrappers) {
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
) []transportSink {
	target, ok := resolveCallable(call.Fun, info)
	if !ok {
		return nil
	}
	function := target.function
	sinks := directTransportSinks(function, target.receiver)
	if sinks == nil {
		sinks = wrappers[function]
	}
	return offsetSinks(sinks, target.argumentOffset)
}

func directTransportSinks(function *types.Func, receiver types.Type) []transportSink {
	if function.Pkg() != nil && (function.Pkg().Path() == echoPackagePath || isCompatibleLocalEchoInterfaceMethod(function, receiver)) {
		switch function.Name() {
		case "Bind":
			return []transportSink{{argument: 0, kind: "request", rule: namedExportedRequestRoot}}
		case "JSON", "JSONPretty":
			return []transportSink{{argument: 1, kind: "response", rule: namedExportedResponseRoot}}
		}
	}
	if isImmichRequestMarshal(function) {
		return []transportSink{{argument: 0, kind: "Immich request", rule: namedDependencyRequestRoot}}
	}
	if isImmichClientMethod(function) || isCompatibleImmichInterfaceMethod(function, receiver) {
		switch function.Name() {
		case "getJSON":
			return []transportSink{{argument: 2, kind: "Immich response", rule: namedDependencyResponseRoot}}
		case "getJSONQuery":
			return []transportSink{{argument: 3, kind: "Immich response", rule: namedDependencyResponseRoot}}
		case "doJSON", "doJSONStatus":
			return []transportSink{
				{argument: 4, kind: "Immich request", rule: namedDependencyRequestRoot},
				{argument: 5, kind: "Immich response", rule: namedDependencyResponseRoot},
			}
		}
	}
	return nil
}

func isCompatibleLocalEchoInterfaceMethod(function *types.Func, receiver types.Type) bool {
	if function.Pkg() == nil || function.Pkg().Path() == echoPackagePath || !isInterfaceType(receiver) {
		return false
	}
	signature, _ := function.Type().(*types.Signature)
	if signature == nil || signature.Variadic() || !isSingleErrorResult(signature.Results()) {
		return false
	}
	params := signature.Params()
	switch function.Name() {
	case "Bind":
		return params.Len() == 1 && isEmptyInterface(params.At(0).Type())
	case "JSON":
		return params.Len() == 2 && isIntType(params.At(0).Type()) && isEmptyInterface(params.At(1).Type())
	case "JSONPretty":
		return params.Len() == 3 && isIntType(params.At(0).Type()) && isEmptyInterface(params.At(1).Type()) && isStringType(params.At(2).Type())
	default:
		return false
	}
}

func isCompatibleImmichInterfaceMethod(function *types.Func, receiver types.Type) bool {
	if function.Pkg() == nil || !isImmichPackage(function.Pkg().Path()) || !isInterfaceType(receiver) {
		return false
	}
	clientName, _ := function.Pkg().Scope().Lookup("Client").(*types.TypeName)
	if clientName == nil {
		return false
	}
	clientType, _ := clientName.Type().(*types.Named)
	if clientType == nil {
		return false
	}
	object, _, _ := types.LookupFieldOrMethod(types.NewPointer(clientType), true, function.Pkg(), function.Name())
	clientMethod, _ := object.(*types.Func)
	return clientMethod != nil && identicalCallableSignatures(function, clientMethod)
}

func identicalCallableSignatures(left, right *types.Func) bool {
	leftSignature, _ := left.Type().(*types.Signature)
	rightSignature, _ := right.Type().(*types.Signature)
	if leftSignature == nil || rightSignature == nil || leftSignature.Variadic() != rightSignature.Variadic() {
		return false
	}
	return identicalTuple(leftSignature.Params(), rightSignature.Params()) &&
		identicalTuple(leftSignature.Results(), rightSignature.Results())
}

func identicalTuple(left, right *types.Tuple) bool {
	if left.Len() != right.Len() {
		return false
	}
	for index := range left.Len() {
		if !types.Identical(left.At(index).Type(), right.At(index).Type()) {
			return false
		}
	}
	return true
}

func isInterfaceType(root types.Type) bool {
	if root == nil {
		return false
	}
	_, ok := types.Unalias(root).Underlying().(*types.Interface)
	return ok
}

func isSingleErrorResult(results *types.Tuple) bool {
	if results.Len() != 1 {
		return false
	}
	errorType := types.Universe.Lookup("error").Type()
	return types.Identical(results.At(0).Type(), errorType)
}

func isEmptyInterface(root types.Type) bool {
	iface, ok := types.Unalias(root).Underlying().(*types.Interface)
	return ok && iface.Empty()
}

func isIntType(root types.Type) bool {
	basic, ok := types.Unalias(root).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.Int
}

func isStringType(root types.Type) bool {
	basic, ok := types.Unalias(root).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.String
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

func resolveCallable(expression ast.Expr, info *types.Info) (callableTarget, bool) {
	switch expression := unparenthesized(expression).(type) {
	case *ast.Ident:
		function, _ := info.Uses[expression].(*types.Func)
		return callableTarget{function: function}, function != nil
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
			return callableTarget{function: function, receiver: selection.Recv(), argumentOffset: offset}, true
		}
		function, _ := info.Uses[expression.Sel].(*types.Func)
		return callableTarget{function: function}, function != nil
	case *ast.IndexExpr:
		return resolveCallable(expression.X, info)
	case *ast.IndexListExpr:
		return resolveCallable(expression.X, info)
	default:
		return callableTarget{}, false
	}
}

func protectedFunctionValueDiagnostics(root string, loaded []*packages.Package, wrappers map[*types.Func][]transportSink) []string {
	var diagnostics []string
	for _, pkg := range loaded {
		for _, file := range pkg.Syntax {
			filename := pkg.Fset.Position(file.Pos()).Filename
			if strings.HasSuffix(filename, "_test.go") {
				continue
			}
			allowed := make(map[ast.Expr]struct{})
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				reference := callableReference(call.Fun)
				target, ok := resolveCallable(reference, pkg.TypesInfo)
				if ok && isProtectedTransportFunction(target, wrappers) {
					allowed[reference] = struct{}{}
				}
				return true
			})
			ast.Inspect(file, func(node ast.Node) bool {
				var expression ast.Expr
				var target callableTarget
				switch node := node.(type) {
				case *ast.SelectorExpr:
					expression = node
					target, _ = resolveCallable(node, pkg.TypesInfo)
				case *ast.Ident:
					expression = node
					target.function, _ = pkg.TypesInfo.Uses[node].(*types.Func)
				default:
					return true
				}
				if target.function == nil || !isProtectedTransportFunction(target, wrappers) {
					return true
				}
				if _, ok := allowed[expression]; ok {
					return nodeTypeMayContainCallableChild(node)
				}
				position := pkg.Fset.Position(expression.Pos())
				diagnostics = append(diagnostics, fmt.Sprintf(
					"%s:%d:%d: protected JSON transport function %s must be called directly; function values must not be passed, stored, or returned",
					relativePath(root, position.Filename), position.Line, position.Column, target.function.Name(),
				))
				return nodeTypeMayContainCallableChild(node)
			})
		}
	}
	return diagnostics
}

func callableReference(expression ast.Expr) ast.Expr {
	switch expression := unparenthesized(expression).(type) {
	case *ast.IndexExpr:
		return callableReference(expression.X)
	case *ast.IndexListExpr:
		return callableReference(expression.X)
	default:
		return expression
	}
}

func isProtectedTransportFunction(target callableTarget, wrappers map[*types.Func][]transportSink) bool {
	return len(directTransportSinks(target.function, target.receiver)) > 0 || len(wrappers[target.function]) > 0
}

func nodeTypeMayContainCallableChild(node ast.Node) bool {
	_, selector := node.(*ast.SelectorExpr)
	return !selector
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
	if named.Obj().Exported() {
		return "exported provider struct " + named.Obj().Name()
	}
	return ""
}

func describeInvalidDependencyResponseRoot(root types.Type) string {
	root = types.Unalias(root)
	switch root := root.(type) {
	case *types.Pointer:
		return describeInvalidDependencyResponseRoot(root.Elem())
	case *types.Named:
		underlying := root.Underlying()
		if _, ok := underlying.(*types.Struct); ok {
			if root.Obj().Pkg() == nil || !isImmichPackage(root.Obj().Pkg().Path()) {
				return "external struct " + root.Obj().Name()
			}
			if root.Obj().Exported() {
				return "exported provider struct " + root.Obj().Name()
			}
			return ""
		}
		return describeInvalidDependencyResponseRoot(underlying)
	case *types.Slice:
		if isByteType(root.Elem()) {
			return "raw bytes"
		}
		return describeInvalidDependencyResponseRoot(root.Elem())
	case *types.Array:
		return describeInvalidDependencyResponseRoot(root.Elem())
	default:
		return typeCategory(root)
	}
}

func isByteType(root types.Type) bool {
	root = types.Unalias(root)
	basic, ok := root.(*types.Basic)
	return ok && basic.Kind() == types.Byte
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

func contractGraphDiagnostics(root string, loaded []*packages.Package, roots []enforcedRoot) []string {
	var diagnostics []string
	visited := make(map[string]map[types.Type]struct{})
	for _, enforced := range roots {
		mode := string(enforced.rule)
		if visited[mode] == nil {
			visited[mode] = make(map[types.Type]struct{})
		}
		visitContractType(root, loaded, enforced.typeOf, enforced.rule, "", "", token.NoPos, visited[mode], &diagnostics)
	}
	return diagnostics
}

func visitContractType(
	root string,
	loaded []*packages.Package,
	typeOf types.Type,
	rule rootRule,
	ownerName string,
	fieldName string,
	fieldPosition token.Pos,
	visited map[types.Type]struct{},
	diagnostics *[]string,
) {
	if typeOf == nil {
		return
	}
	typeOf = types.Unalias(typeOf)
	switch current := typeOf.(type) {
	case *types.Pointer:
		visitContractType(root, loaded, current.Elem(), rule, ownerName, fieldName, fieldPosition, visited, diagnostics)
	case *types.Slice:
		visitContractType(root, loaded, current.Elem(), rule, ownerName, fieldName, fieldPosition, visited, diagnostics)
	case *types.Array:
		visitContractType(root, loaded, current.Elem(), rule, ownerName, fieldName, fieldPosition, visited, diagnostics)
	case *types.Map:
		if !isMementoRoot(rule) {
			appendGraphDiagnostic(root, loaded, fieldPosition, rule, ownerName, fieldName, "map", diagnostics)
			return
		}
		visitContractType(root, loaded, current.Key(), rule, ownerName, fieldName, fieldPosition, visited, diagnostics)
		visitContractType(root, loaded, current.Elem(), rule, ownerName, fieldName, fieldPosition, visited, diagnostics)
	case *types.Interface:
		appendGraphDiagnostic(root, loaded, fieldPosition, rule, ownerName, fieldName, "interface", diagnostics)
	case *types.Struct:
		appendGraphDiagnostic(root, loaded, fieldPosition, rule, ownerName, fieldName, "anonymous struct", diagnostics)
	case *types.TypeParam:
		appendGraphDiagnostic(root, loaded, fieldPosition, rule, ownerName, fieldName, "unresolved type parameter", diagnostics)
	case *types.Named:
		if _, ok := visited[current]; ok {
			return
		}
		visited[current] = struct{}{}
		underlying := current.Underlying()
		if _, scalar := underlying.(*types.Basic); scalar {
			return
		}
		if structure, ok := underlying.(*types.Struct); ok {
			if !isMementoRoot(rule) {
				object := current.Obj()
				if object.Pkg() == nil || !isImmichPackage(object.Pkg().Path()) {
					if isSerializedValueType(current, rule) {
						return
					}
					appendGraphDiagnostic(root, loaded, fieldPosition, rule, ownerName, fieldName, "external struct "+object.Name(), diagnostics)
					return
				}
				if object.Exported() {
					appendGraphDiagnostic(root, loaded, fieldPosition, rule, ownerName, fieldName, "exported provider struct "+object.Name(), diagnostics)
					return
				}
			}
			visitContractStruct(root, loaded, current.Obj().Name(), structure, rule, visited, diagnostics)
			return
		}
		visitContractType(root, loaded, underlying, rule, ownerName, fieldName, fieldPosition, visited, diagnostics)
	case *types.Basic:
		return
	default:
		appendGraphDiagnostic(root, loaded, fieldPosition, rule, ownerName, fieldName, typeCategory(current), diagnostics)
	}
}

func visitContractStruct(
	root string,
	loaded []*packages.Package,
	ownerName string,
	structure *types.Struct,
	rule rootRule,
	visited map[types.Type]struct{},
	diagnostics *[]string,
) {
	for index := range structure.NumFields() {
		field := structure.Field(index)
		if !isJSONField(field, structure.Tag(index)) {
			continue
		}
		visitContractType(root, loaded, field.Type(), rule, ownerName, field.Name(), field.Pos(), visited, diagnostics)
	}
}

func appendGraphDiagnostic(
	root string,
	loaded []*packages.Package,
	positionToken token.Pos,
	rule rootRule,
	ownerName string,
	fieldName string,
	problem string,
	diagnostics *[]string,
) {
	if ownerName == "" || fieldName == "" {
		return
	}
	position := sourcePosition(loaded, positionToken)
	kind := "request"
	switch rule {
	case namedExportedRequestRoot:
	case namedExportedResponseRoot:
		kind = "response"
	case namedDependencyRequestRoot:
		kind = "Immich request"
	case namedDependencyResponseRoot:
		kind = "Immich response"
	}
	*diagnostics = append(*diagnostics, fmt.Sprintf(
		"%s:%d:%d: %s JSON contract graph %s.%s must not contain %s",
		relativePath(root, position.Filename), position.Line, position.Column, kind, ownerName, fieldName, problem,
	))
}

func rawMessageFieldDiagnostics(root string, loaded []*packages.Package, roots []enforcedRoot) []string {
	var diagnostics []string
	visited := make(map[types.Type]struct{})
	var visit func(types.Type)
	visit = func(typeOf types.Type) {
		if typeOf == nil {
			return
		}
		typeOf = types.Unalias(typeOf)
		if _, ok := visited[typeOf]; ok {
			return
		}
		visited[typeOf] = struct{}{}
		switch current := typeOf.(type) {
		case *types.Pointer:
			visit(current.Elem())
		case *types.Slice:
			visit(current.Elem())
		case *types.Array:
			visit(current.Elem())
		case *types.Map:
			visit(current.Key())
			visit(current.Elem())
		case *types.Named:
			object := current.Obj()
			if object.Pkg() == nil || !isImmichPackage(object.Pkg().Path()) {
				return
			}
			structure, ok := current.Underlying().(*types.Struct)
			if !ok {
				visit(current.Underlying())
				return
			}
			for index := range structure.NumFields() {
				field := structure.Field(index)
				if !isJSONField(field, structure.Tag(index)) {
					continue
				}
				if containsJSONRawMessage(field.Type()) {
					position := sourcePosition(loaded, field.Pos())
					diagnostics = append(diagnostics, fmt.Sprintf(
						"%s:%d:%d: Immich provider DTO %s.%s must not contain json.RawMessage fields",
						relativePath(root, position.Filename), position.Line, position.Column, object.Name(), field.Name(),
					))
					continue
				}
				visit(field.Type())
			}
		}
	}
	for _, enforced := range roots {
		if !isMementoRoot(enforced.rule) {
			visit(enforced.typeOf)
		}
	}
	return diagnostics
}

func isJSONField(field *types.Var, tag string) bool {
	name := reflect.StructTag(tag).Get("json")
	if index := strings.IndexByte(name, ','); index >= 0 {
		name = name[:index]
	}
	if name == "-" {
		return false
	}
	if field.Exported() {
		return true
	}
	if !field.Anonymous() {
		return false
	}
	embedded := types.Unalias(field.Type())
	if pointer, ok := embedded.(*types.Pointer); ok {
		embedded = types.Unalias(pointer.Elem())
	}
	_, isStruct := embedded.Underlying().(*types.Struct)
	return isStruct
}

func sourcePosition(loaded []*packages.Package, position token.Pos) token.Position {
	for _, pkg := range loaded {
		candidate := pkg.Fset.Position(position)
		if candidate.IsValid() && candidate.Filename != "" {
			return candidate
		}
	}
	return token.Position{}
}

func isMementoRoot(rule rootRule) bool {
	return rule == namedExportedRequestRoot || rule == namedExportedResponseRoot
}

func isSerializedValueType(named *types.Named, rule rootRule) bool {
	methodNames := []string{"MarshalJSON", "MarshalText"}
	if rule == namedDependencyResponseRoot {
		methodNames = []string{"UnmarshalJSON", "UnmarshalText"}
	}
	for _, receiver := range []types.Type{named, types.NewPointer(named)} {
		methodSet := types.NewMethodSet(receiver)
		for _, name := range methodNames {
			selection := methodSet.Lookup(nil, name)
			if selection != nil {
				return true
			}
		}
	}
	return false
}

func containsJSONRawMessage(root types.Type) bool {
	root = types.Unalias(root)
	if named, ok := root.(*types.Named); ok {
		if named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "encoding/json" && named.Obj().Name() == "RawMessage" {
			return true
		}
		for index := range named.TypeArgs().Len() {
			if containsJSONRawMessage(named.TypeArgs().At(index)) {
				return true
			}
		}
		return false
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
