import { readFileSync } from "node:fs";
import path from "node:path";
import process from "node:process";
import { pathToFileURL } from "node:url";

import ts from "typescript";

const sharedAPIFunctions = new Set(["apiJSON", "apiNoContent", "apiResponse"]);
const assignmentOperators = new Set([
  ts.SyntaxKind.EqualsToken,
  ts.SyntaxKind.PlusEqualsToken,
  ts.SyntaxKind.MinusEqualsToken,
  ts.SyntaxKind.AsteriskEqualsToken,
  ts.SyntaxKind.AsteriskAsteriskEqualsToken,
  ts.SyntaxKind.SlashEqualsToken,
  ts.SyntaxKind.PercentEqualsToken,
  ts.SyntaxKind.LessThanLessThanEqualsToken,
  ts.SyntaxKind.GreaterThanGreaterThanEqualsToken,
  ts.SyntaxKind.GreaterThanGreaterThanGreaterThanEqualsToken,
  ts.SyntaxKind.AmpersandEqualsToken,
  ts.SyntaxKind.BarEqualsToken,
  ts.SyntaxKind.CaretEqualsToken,
  ts.SyntaxKind.BarBarEqualsToken,
  ts.SyntaxKind.AmpersandAmpersandEqualsToken,
  ts.SyntaxKind.QuestionQuestionEqualsToken,
]);
const maximumAnalysisDepth = 40;

export function checkTypeScriptContracts(repositoryRoot, options = {}) {
  const root = path.resolve(repositoryRoot);
  const appRoot = path.join(root, "app");
  const apiPath = path.join(appRoot, "api.ts");
  const generatedOutputs = readTygoOutputPaths(root);
  const configPath = findConfig(root);
  const config = ts.readConfigFile(configPath, ts.sys.readFile);
  if (config.error) throw new Error(formatCompilerDiagnostic(config.error));

  const parsed = ts.parseJsonConfigFileContent(
    config.config,
    ts.sys,
    path.dirname(configPath),
    undefined,
    configPath,
  );
  if (parsed.errors.length > 0) {
    throw new Error(parsed.errors.map(formatCompilerDiagnostic).join("\n"));
  }

  const program = ts.createProgram({
    rootNames: parsed.fileNames,
    options: parsed.options,
  });
  const checker = program.getTypeChecker();
  const included = options.include
    ? new Set(options.include.map((entry) => normalizePath(entry)))
    : undefined;
  const sourceFiles = program
    .getSourceFiles()
    .filter((sourceFile) =>
      isProductionAppSource(sourceFile, appRoot, included),
    );
  const context = {
    apiPath,
    checker,
    generatedOutputs,
    sourceFiles,
    unsafeObjectSymbols: collectUnsafeObjectSymbols(
      sourceFiles,
      checker,
      apiPath,
    ),
  };
  const diagnostics = new Set();

  for (const sourceFile of sourceFiles) {
    const sourcePath = path.resolve(sourceFile.fileName);
    const isAPIFile = sourcePath === apiPath;
    const relative = normalizePath(path.relative(root, sourcePath));
    const report = (node, code, message) => {
      const start = sourceFile.getLineAndCharacterOfPosition(
        node.getStart(sourceFile),
      );
      diagnostics.add(
        `${relative}:${start.line + 1}:${start.character + 1} [${code}]: ${message}`,
      );
    };

    if (isAPIFile) {
      reportUnauthorizedAPITransportFunctions(sourceFile, report, context);
    }

    const visit = (node) => {
      if (
        ts.isImportDeclaration(node) &&
        node.importClause?.namedBindings &&
        ts.isNamespaceImport(node.importClause.namedBindings) &&
        namespaceImportTargetsAPI(node.importClause.namedBindings, context)
      ) {
        report(
          node.importClause.namedBindings,
          "shared-api-namespace",
          "namespace imports from app/api.ts are forbidden; import its functions by name",
        );
      }

      if (ts.isCallExpression(node) && !isAPIFile) {
        if (isDOMResponseJSONCall(node, context.checker)) {
          report(
            node.expression,
            "response-json",
            "decode JSON HTTP responses with apiJSON instead of Response.json()",
          );
        }

        const sharedName = directSharedAPIName(node.expression, context);
        if (isAllowedDirectSharedCall(node.expression, sharedName)) {
          if (sharedName === "apiJSON") {
            checkResponseContract(node, report, context);
          }
          if (node.arguments.length >= 2) {
            checkRequestContract(node.arguments[1], report, context);
          }
        }
      }

      if (
        isReferenceExpression(node) &&
        !isNestedReferenceName(node) &&
        !declarationName(node)
      ) {
        const sharedAccess = sharedAPIAccess(node, context);
        if (
          sharedAccess &&
          !isImportReference(node) &&
          (!isDirectCallTarget(node) ||
            sharedAccess.computed ||
            referenceName(node) !== sharedAccess.name)
        ) {
          const message = sharedAccess.name
            ? `${sharedAccess.name} must be called directly and cannot be stored, passed, returned, rebound, or wrapped${sharedAccess.computed ? "; element access is forbidden" : ""}`
            : "computed access to shared API exports is forbidden because the key cannot be resolved safely";
          report(node, "shared-api-indirection", message);
        }

        const fetchAccess = globalFetchAccess(node, checker);
        if (fetchAccess) {
          if (!isAPIFile) {
            report(
              node,
              "direct-fetch",
              "global fetch is only allowed in app/api.ts",
            );
          } else if (!isAllowedAPIGlobalFetch(node, fetchAccess, sourceFile)) {
            const declaration = containingFunction(node);
            if (
              !declaration ||
              isApprovedAPITransportFunction(declaration, sourceFile)
            ) {
              report(
                node,
                "api-transport-surface",
                "only apiResponse, apiJSON, and apiNoContent may use or expose the app/api.ts transport flow",
              );
            }
          }
        }
      }

      if (ts.isBindingElement(node)) {
        const propertySymbol = bindingElementPropertySymbol(node, checker);
        const sharedName = sharedAPINameForSymbol(propertySymbol, context);
        if (sharedName) {
          report(
            node.propertyName ?? node.name,
            "shared-api-indirection",
            `${sharedName} must be called directly and cannot be stored, passed, returned, rebound, or wrapped`,
          );
        }
        if (propertySymbol && isGlobalFetchSymbol(propertySymbol)) {
          report(
            node.propertyName ?? node.name,
            isAPIFile ? "api-transport-surface" : "direct-fetch",
            isAPIFile
              ? "only apiResponse, apiJSON, and apiNoContent may use or expose the app/api.ts transport flow"
              : "global fetch is only allowed in app/api.ts",
          );
        }
      }

      ts.forEachChild(node, visit);
    };
    visit(sourceFile);
  }

  return [...diagnostics].sort();
}

function reportUnauthorizedAPITransportFunctions(sourceFile, report, context) {
  const exported = exportedFunctionLikes(sourceFile, context.checker);
  const functions = [];
  const collect = (node) => {
    if (ts.isFunctionLike(node) && node.body) functions.push(node);
    ts.forEachChild(node, collect);
  };
  collect(sourceFile);

  for (const declaration of functions) {
    if (isApprovedAPITransportFunction(declaration, sourceFile)) continue;
    const hasTransport = containsTransportFlow(
      declaration.body,
      context,
      new Set([declaration]),
    );
    const hasExportedSerialization =
      exported.has(declaration) &&
      containsJSONStringify(declaration.body, context.checker);
    if (!hasTransport && !hasExportedSerialization) continue;
    report(
      functionLikeName(declaration),
      "api-transport-surface",
      "only apiResponse, apiJSON, and apiNoContent may use or expose the app/api.ts transport flow",
    );
  }
}

function containsTransportFlow(node, context, seen) {
  let found = false;
  const visit = (current) => {
    if (found) return;
    if (current !== node && ts.isFunctionLike(current)) return;
    if (
      isReferenceExpression(current) &&
      !isNestedReferenceName(current) &&
      !declarationName(current) &&
      (sharedAPIAccess(current, context) ||
        globalFetchAccess(current, context.checker))
    ) {
      found = true;
      return;
    }
    if (ts.isCallExpression(current)) {
      const called = calledFunctionLike(current.expression, context.checker);
      if (
        called &&
        !seen.has(called) &&
        path.resolve(called.getSourceFile().fileName) === context.apiPath
      ) {
        seen.add(called);
        if (containsTransportFlow(called.body, context, seen)) {
          found = true;
          return;
        }
      }
    }
    ts.forEachChild(current, visit);
  };
  visit(node);
  return found;
}

function calledFunctionLike(expression, checker) {
  const symbol = expressionSymbol(unwrapExpression(expression), checker);
  for (const declaration of symbol?.getDeclarations() ?? []) {
    if (ts.isFunctionLike(declaration) && declaration.body) return declaration;
    if (
      ts.isVariableDeclaration(declaration) &&
      declaration.initializer &&
      ts.isFunctionLike(declaration.initializer) &&
      declaration.initializer.body
    ) {
      return declaration.initializer;
    }
  }
  return undefined;
}

function containsJSONStringify(node, checker) {
  let found = false;
  const visit = (current) => {
    if (found) return;
    if (current !== node && ts.isFunctionLike(current)) return;
    if (isJSONStringifyCall(current, checker)) {
      found = true;
      return;
    }
    ts.forEachChild(current, visit);
  };
  visit(node);
  return found;
}

function exportedFunctionLikes(sourceFile, checker) {
  const result = new Set();
  const moduleSymbol = checker.getSymbolAtLocation(sourceFile);
  for (const exportedSymbol of moduleSymbol
    ? checker.getExportsOfModule(moduleSymbol)
    : []) {
    const symbol = resolveAliasSymbol(exportedSymbol, checker);
    for (const declaration of symbol?.getDeclarations() ?? []) {
      if (ts.isFunctionLike(declaration) && declaration.body) {
        result.add(declaration);
      } else if (
        ts.isVariableDeclaration(declaration) &&
        declaration.initializer &&
        ts.isFunctionLike(declaration.initializer)
      ) {
        result.add(declaration.initializer);
      }
    }
  }
  return result;
}

function functionLikeName(declaration) {
  if (declaration.name) return declaration.name;
  if (
    ts.isVariableDeclaration(declaration.parent) &&
    ts.isIdentifier(declaration.parent.name)
  ) {
    return declaration.parent.name;
  }
  return declaration;
}

function isApprovedAPITransportFunction(declaration, sourceFile) {
  return (
    ts.isFunctionDeclaration(declaration) &&
    declaration.parent === sourceFile &&
    declaration.name &&
    sharedAPIFunctions.has(declaration.name.text) &&
    declaration.modifiers?.some(
      (modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword,
    )
  );
}

function containingFunction(node) {
  for (let current = node.parent; current; current = current.parent) {
    if (ts.isFunctionLike(current)) return current;
  }
  return undefined;
}

function isAllowedAPIGlobalFetch(node, access, sourceFile) {
  const declaration = containingFunction(node);
  return Boolean(
    declaration &&
    isApprovedAPITransportFunction(declaration, sourceFile) &&
    !access.computed &&
    isDirectCallTarget(node) &&
    referenceName(node) === "fetch",
  );
}

function checkResponseContract(call, report, context) {
  if (call.typeArguments?.length !== 1) {
    report(
      call.expression,
      "response-contract",
      "apiJSON must declare one response type from a configured Tygo output",
    );
    return;
  }

  const responseTypeNode = call.typeArguments[0];
  const responseType = context.checker.getTypeFromTypeNode(responseTypeNode);
  if (
    !hasGeneratedDeclaration(
      responseType,
      context.generatedOutputs,
      context.checker,
    )
  ) {
    report(
      responseTypeNode,
      "response-contract",
      `apiJSON response type ${context.checker.typeToString(responseType)} must be declared in a configured Tygo output`,
    );
  }
}

function checkRequestContract(init, report, context) {
  const options = requestBodies(init, context, new Set(), 0);
  if (options.unknown) {
    report(
      init,
      "request-contract",
      "shared API request options must be an immutable object-literal, conditional, or spread graph",
    );
    return;
  }

  for (const body of options.bodies) {
    const serialized = serializedPayloads(body, context, new Set(), 0);
    if (serialized.unknown) {
      report(
        body,
        "request-contract",
        "shared API request body could not be resolved deterministically",
      );
      continue;
    }
    if (serialized.payloads.length === 0 && !serialized.absent) {
      report(
        body,
        "request-contract",
        "shared API request body must serialize a payload with generated-type provenance",
      );
    }
    for (const payload of serialized.payloads) {
      if (!hasGeneratedProvenance(payload, context, new Set(), 0)) {
        report(
          payload,
          "request-contract",
          "shared API request payload must have generated-type provenance",
        );
      }
    }
  }
}

function requestBodies(expression, context, seen, depth) {
  if (depth > maximumAnalysisDepth) return unknownBodies();
  expression = unwrapExpression(expression);
  if (isAbsent(expression)) return knownBodies();

  if (ts.isObjectLiteralExpression(expression)) {
    const result = knownBodies();
    for (const property of expression.properties) {
      if (
        ts.isPropertyAssignment(property) &&
        propertyName(property.name) === "body"
      ) {
        result.bodies.push(property.initializer);
      } else if (
        ts.isShorthandPropertyAssignment(property) &&
        property.name.text === "body"
      ) {
        result.bodies.push(property.name);
      } else if (ts.isSpreadAssignment(property)) {
        mergeBodies(
          result,
          requestBodies(property.expression, context, new Set(seen), depth + 1),
        );
      } else if (
        property.name &&
        (propertyName(property.name) === "body" ||
          propertyName(property.name) === undefined)
      ) {
        result.unknown = true;
      }
    }
    return result;
  }

  if (ts.isConditionalExpression(expression)) {
    return mergeBodies(
      requestBodies(expression.whenTrue, context, new Set(seen), depth + 1),
      requestBodies(expression.whenFalse, context, new Set(seen), depth + 1),
    );
  }

  const initializer = immutableConstInitializer(expression, context, seen);
  if (initializer) {
    return requestBodies(initializer, context, seen, depth + 1);
  }

  return unknownBodies();
}

function serializedPayloads(expression, context, seen, depth) {
  if (depth > maximumAnalysisDepth) return unknownPayloads();
  expression = unwrapExpression(expression);
  if (isAbsent(expression)) {
    return { absent: true, payloads: [], unknown: false };
  }
  if (isJSONStringifyCall(expression, context.checker)) {
    return {
      absent: false,
      payloads:
        expression.arguments.length > 0 ? [expression.arguments[0]] : [],
      unknown: expression.arguments.length === 0,
    };
  }
  if (ts.isConditionalExpression(expression)) {
    return mergePayloads(
      serializedPayloads(
        expression.whenTrue,
        context,
        new Set(seen),
        depth + 1,
      ),
      serializedPayloads(
        expression.whenFalse,
        context,
        new Set(seen),
        depth + 1,
      ),
    );
  }

  const initializer = immutableConstInitializer(expression, context, seen);
  if (initializer) {
    return serializedPayloads(initializer, context, seen, depth + 1);
  }

  return { absent: false, payloads: [], unknown: false };
}

function immutableConstInitializer(expression, context, seen) {
  if (!ts.isIdentifier(expression)) return undefined;
  const symbol = expressionSymbol(expression, context.checker);
  if (!symbol || seen.has(symbol) || context.unsafeObjectSymbols.has(symbol)) {
    return undefined;
  }
  seen.add(symbol);
  const declarations = symbol.getDeclarations() ?? [];
  if (declarations.length !== 1) return undefined;
  const declaration = declarations[0];
  if (
    !ts.isVariableDeclaration(declaration) ||
    !declaration.initializer ||
    !isConstVariableDeclaration(declaration)
  ) {
    return undefined;
  }
  return declaration.initializer;
}

function hasGeneratedProvenance(expression, context, seen, depth) {
  if (depth > maximumAnalysisDepth) return false;
  expression = unwrapParentheses(expression);

  if (
    ts.isSatisfiesExpression(expression) ||
    ts.isAsExpression(expression) ||
    ts.isTypeAssertionExpression(expression)
  ) {
    return hasGeneratedDeclaration(
      context.checker.getTypeFromTypeNode(expression.type),
      context.generatedOutputs,
      context.checker,
    );
  }

  if (
    hasGeneratedDeclaration(
      context.checker.getTypeAtLocation(expression),
      context.generatedOutputs,
      context.checker,
    )
  ) {
    return true;
  }

  const symbol = expressionSymbol(expression, context.checker);
  if (symbol && !seen.has(symbol)) {
    seen.add(symbol);
    for (const declaration of symbol.getDeclarations() ?? []) {
      if (
        declaration.type &&
        hasGeneratedDeclaration(
          context.checker.getTypeFromTypeNode(declaration.type),
          context.generatedOutputs,
          context.checker,
        )
      ) {
        return true;
      }
      const value = declarationValue(declaration);
      if (
        value &&
        hasGeneratedProvenance(value, context, new Set(seen), depth + 1)
      ) {
        return true;
      }
    }
  }

  if (ts.isCallExpression(expression)) {
    const signature = context.checker.getResolvedSignature(expression);
    return Boolean(
      signature &&
      hasGeneratedDeclaration(
        context.checker.getReturnTypeOfSignature(signature),
        context.generatedOutputs,
        context.checker,
      ),
    );
  }

  return false;
}

function collectUnsafeObjectSymbols(sourceFiles, checker, apiPath) {
  const unsafe = new Set();
  const aliases = [];

  const markRoot = (expression) => {
    const symbol = rootExpressionSymbol(expression, checker);
    if (symbol) unsafe.add(symbol);
  };

  for (const sourceFile of sourceFiles) {
    if (path.resolve(sourceFile.fileName) === apiPath) continue;
    const visit = (node) => {
      if (
        ts.isBinaryExpression(node) &&
        assignmentOperators.has(node.operatorToken.kind)
      ) {
        markRoot(node.left);
      } else if (
        (ts.isPrefixUnaryExpression(node) ||
          ts.isPostfixUnaryExpression(node)) &&
        [ts.SyntaxKind.PlusPlusToken, ts.SyntaxKind.MinusMinusToken].includes(
          node.operator,
        )
      ) {
        markRoot(node.operand);
      } else if (ts.isDeleteExpression(node) || ts.isReturnStatement(node)) {
        if (node.expression) markRoot(node.expression);
      } else if (ts.isVariableDeclaration(node) && node.initializer) {
        const source = rootExpressionSymbol(node.initializer, checker, {
          referencesOnly: true,
        });
        if (source) {
          for (const target of bindingSymbols(node.name, checker)) {
            aliases.push([target, source]);
          }
        }
      } else if (ts.isCallExpression(node)) {
        if (ts.isPropertyAccessExpression(node.expression)) {
          markRoot(node.expression.expression);
        }
        const directShared = directSharedAPIName(node.expression, {
          apiPath,
          checker,
        });
        node.arguments.forEach((argument, index) => {
          if (!(directShared && index === 1)) markRoot(argument);
        });
      }
      ts.forEachChild(node, visit);
    };
    visit(sourceFile);
  }

  let changed = true;
  while (changed) {
    changed = false;
    for (const [left, right] of aliases) {
      if (unsafe.has(left) && !unsafe.has(right)) {
        unsafe.add(right);
        changed = true;
      }
      if (unsafe.has(right) && !unsafe.has(left)) {
        unsafe.add(left);
        changed = true;
      }
    }
  }
  return unsafe;
}

function namespaceImportTargetsAPI(namespaceImport, context) {
  const symbol = resolveAliasSymbol(
    context.checker.getSymbolAtLocation(namespaceImport.name),
    context.checker,
  );
  return (symbol?.getDeclarations() ?? []).some(
    (declaration) =>
      path.resolve(declaration.getSourceFile().fileName) === context.apiPath,
  );
}

function isDOMResponseJSONCall(call, checker) {
  if (
    !ts.isPropertyAccessExpression(call.expression) ||
    call.expression.name.text !== "json"
  ) {
    return false;
  }

  const responseSymbol = checker.resolveName(
    "Response",
    call.expression.expression,
    ts.SymbolFlags.Type,
    false,
  );
  if (!responseSymbol || !isDOMResponseSymbol(responseSymbol)) return false;

  return typeIncludesDOMResponse(
    checker.getTypeAtLocation(call.expression.expression),
    checker.getDeclaredTypeOfSymbol(responseSymbol),
    checker,
  );
}

function isDOMResponseSymbol(symbol) {
  return (symbol.getDeclarations() ?? []).some(
    (declaration) =>
      declarationSymbolName(declaration) === "Response" &&
      /(^|\/)lib\.dom\.d\.ts$/.test(
        normalizePath(declaration.getSourceFile().fileName),
      ),
  );
}

function typeIncludesDOMResponse(type, responseType, checker) {
  if (type.isUnion() || type.isIntersection()) {
    return type.types.some((part) =>
      typeIncludesDOMResponse(part, responseType, checker),
    );
  }
  if (
    (type.flags &
      (ts.TypeFlags.Any |
        ts.TypeFlags.Unknown |
        ts.TypeFlags.Never |
        ts.TypeFlags.Undefined |
        ts.TypeFlags.Null)) !==
    0
  ) {
    return false;
  }
  return checker.isTypeAssignableTo(type, responseType);
}

function directSharedAPIName(expression, context) {
  expression = unwrapExpression(expression);
  return sharedAPINameForExpression(expression, context);
}

function sharedAPIAccess(expression, context) {
  const directName = sharedAPINameForExpression(expression, context);
  if (directName) {
    return {
      computed: ts.isElementAccessExpression(expression),
      name: directName,
    };
  }
  if (!ts.isElementAccessExpression(expression)) return undefined;

  const protectedNames = protectedSharedAPINamesOnType(
    expression.expression,
    context,
  );
  if (protectedNames.size === 0) return undefined;
  const key = constantStringValue(
    expression.argumentExpression,
    context.checker,
    new Set(),
    0,
  );
  if (key.known) {
    return protectedNames.has(key.value)
      ? { computed: true, name: key.value }
      : undefined;
  }
  return { computed: true, name: undefined };
}

function protectedSharedAPINamesOnType(expression, context) {
  const names = new Set();
  const type = context.checker.getTypeAtLocation(unwrapExpression(expression));
  for (const property of context.checker.getPropertiesOfType(type)) {
    const name = sharedAPINameForSymbol(
      resolveAliasSymbol(property, context.checker),
      context,
    );
    if (name) names.add(name);
  }
  return names;
}

function globalFetchAccess(expression, checker) {
  if (isGlobalFetchSymbol(expressionSymbol(expression, checker))) {
    return { computed: ts.isElementAccessExpression(expression) };
  }
  if (!ts.isElementAccessExpression(expression)) return undefined;

  const ownerType = checker.getTypeAtLocation(
    unwrapExpression(expression.expression),
  );
  const fetchProperty = checker.getPropertyOfType(ownerType, "fetch");
  if (!isGlobalFetchSymbol(resolveAliasSymbol(fetchProperty, checker))) {
    return undefined;
  }
  const key = constantStringValue(
    expression.argumentExpression,
    checker,
    new Set(),
    0,
  );
  if (key.known && key.value !== "fetch") return undefined;
  return { computed: true };
}

function constantStringValue(expression, checker, seen, depth) {
  if (depth > maximumAnalysisDepth) return { known: false };
  expression = unwrapExpression(expression);
  if (ts.isStringLiteralLike(expression)) {
    return { known: true, value: expression.text };
  }
  if (!ts.isIdentifier(expression)) return { known: false };
  const symbol = expressionSymbol(expression, checker);
  if (!symbol || seen.has(symbol)) return { known: false };
  seen.add(symbol);
  const declarations = symbol.getDeclarations() ?? [];
  if (declarations.length !== 1) return { known: false };
  const declaration = declarations[0];
  if (
    !ts.isVariableDeclaration(declaration) ||
    !declaration.initializer ||
    !isConstVariableDeclaration(declaration)
  ) {
    return { known: false };
  }
  return constantStringValue(declaration.initializer, checker, seen, depth + 1);
}

function sharedAPINameForExpression(expression, context) {
  return sharedAPINameForSymbol(
    expressionSymbol(expression, context.checker),
    context,
  );
}

function sharedAPINameForSymbol(symbol, context) {
  if (!symbol) return undefined;
  for (const declaration of symbol.getDeclarations() ?? []) {
    const name = declarationSymbolName(declaration);
    if (
      path.resolve(declaration.getSourceFile().fileName) === context.apiPath &&
      sharedAPIFunctions.has(name)
    ) {
      return name;
    }
  }
  return undefined;
}

function isAllowedDirectSharedCall(expression, sharedName) {
  expression = unwrapExpression(expression);
  return Boolean(sharedName && referenceName(expression) === sharedName);
}

function referenceName(node) {
  if (ts.isIdentifier(node)) return node.text;
  if (ts.isPropertyAccessExpression(node)) return node.name.text;
  return undefined;
}

function isDirectCallTarget(node) {
  let current = node;
  while (
    current.parent &&
    (ts.isParenthesizedExpression(current.parent) ||
      ts.isAsExpression(current.parent) ||
      ts.isSatisfiesExpression(current.parent) ||
      ts.isTypeAssertionExpression(current.parent) ||
      ts.isNonNullExpression(current.parent))
  ) {
    current = current.parent;
  }
  return (
    ts.isCallExpression(current.parent) && current.parent.expression === current
  );
}

function isReferenceExpression(node) {
  return (
    ts.isIdentifier(node) ||
    ts.isPropertyAccessExpression(node) ||
    ts.isElementAccessExpression(node)
  );
}

function isNestedReferenceName(node) {
  return (
    ts.isIdentifier(node) &&
    ((ts.isPropertyAccessExpression(node.parent) &&
      node.parent.name === node) ||
      (ts.isQualifiedName(node.parent) && node.parent.right === node))
  );
}

function isImportReference(node) {
  for (let current = node; current; current = current.parent) {
    if (
      ts.isImportDeclaration(current) ||
      ts.isImportEqualsDeclaration(current)
    ) {
      return true;
    }
    if (ts.isStatement(current)) return false;
  }
  return false;
}

function bindingElementPropertySymbol(node, checker) {
  if (!ts.isObjectBindingPattern(node.parent)) return undefined;
  const name = propertyName(node.propertyName ?? node.name);
  if (!name) return undefined;
  const owner = node.parent.parent;
  let source;
  if (
    (ts.isVariableDeclaration(owner) || ts.isParameter(owner)) &&
    owner.initializer
  ) {
    source = owner.initializer;
  } else if (ts.isParameter(owner)) {
    source = owner;
  }
  if (!source) return undefined;
  const type = checker.getTypeAtLocation(source);
  const symbol = checker.getPropertyOfType(type, name);
  return resolveAliasSymbol(symbol, checker);
}

function rootExpressionSymbol(expression, checker, options = {}) {
  expression = unwrapExpression(expression);
  if (ts.isPropertyAccessExpression(expression)) {
    return rootExpressionSymbol(expression.expression, checker, options);
  }
  if (ts.isElementAccessExpression(expression)) {
    return rootExpressionSymbol(expression.expression, checker, options);
  }
  if (!ts.isIdentifier(expression)) return undefined;
  if (options.referencesOnly && declarationName(expression)) return undefined;
  return expressionSymbol(expression, checker);
}

function declarationName(node) {
  return (
    (ts.isVariableDeclaration(node.parent) ||
      ts.isParameter(node.parent) ||
      ts.isFunctionDeclaration(node.parent)) &&
    node.parent.name === node
  );
}

function bindingSymbols(name, checker) {
  if (ts.isIdentifier(name)) {
    const symbol = expressionSymbol(name, checker);
    return symbol ? [symbol] : [];
  }
  const symbols = [];
  for (const element of name.elements) {
    if (!ts.isOmittedExpression(element)) {
      symbols.push(...bindingSymbols(element.name, checker));
    }
  }
  return symbols;
}

function expressionSymbol(expression, checker) {
  let symbol =
    ts.isIdentifier(expression) &&
    ts.isShorthandPropertyAssignment(expression.parent) &&
    expression.parent.name === expression
      ? checker.getShorthandAssignmentValueSymbol(expression.parent)
      : checker.getSymbolAtLocation(expression);
  if (
    !symbol &&
    (ts.isPropertyAccessExpression(expression) ||
      ts.isElementAccessExpression(expression))
  ) {
    symbol = checker.getSymbolAtLocation(
      ts.isPropertyAccessExpression(expression)
        ? expression.name
        : expression.argumentExpression,
    );
  }
  return resolveAliasSymbol(symbol, checker);
}

function resolveAliasSymbol(symbol, checker) {
  if (symbol && (symbol.flags & ts.SymbolFlags.Alias) !== 0) {
    return checker.getAliasedSymbol(symbol);
  }
  return symbol;
}

function declarationValue(declaration) {
  if (
    (ts.isVariableDeclaration(declaration) ||
      ts.isPropertyDeclaration(declaration) ||
      ts.isPropertyAssignment(declaration) ||
      ts.isParameter(declaration)) &&
    declaration.initializer
  ) {
    return declaration.initializer;
  }
  if (ts.isBindingElement(declaration) && declaration.initializer) {
    return declaration.initializer;
  }
  if (ts.isShorthandPropertyAssignment(declaration)) return declaration.name;
  return undefined;
}

function hasGeneratedDeclaration(
  type,
  generatedOutputs,
  checker,
  seen = new Set(),
) {
  if (type.isUnion() || type.isIntersection()) {
    const relevant = type.types.filter(
      (part) =>
        (part.flags &
          (ts.TypeFlags.Undefined | ts.TypeFlags.Null | ts.TypeFlags.Never)) ===
        0,
    );
    return (
      relevant.length > 0 &&
      relevant.every((part) =>
        hasGeneratedDeclaration(part, generatedOutputs, checker, new Set(seen)),
      )
    );
  }
  const symbol = type.aliasSymbol ?? type.getSymbol();
  if (!symbol || seen.has(symbol)) return false;
  seen.add(symbol);
  const declarations = symbol.getDeclarations() ?? [];
  if (
    declarations.length > 0 &&
    declarations.every((declaration) =>
      generatedOutputs.has(path.resolve(declaration.getSourceFile().fileName)),
    )
  ) {
    return true;
  }
  return declarations.some(
    (declaration) =>
      ts.isTypeAliasDeclaration(declaration) &&
      hasGeneratedTypeNode(
        declaration.type,
        generatedOutputs,
        checker,
        new Set(seen),
      ),
  );
}

function hasGeneratedTypeNode(node, generatedOutputs, checker, seen) {
  if (ts.isUnionTypeNode(node) || ts.isIntersectionTypeNode(node)) {
    return node.types.every((part) =>
      hasGeneratedTypeNode(part, generatedOutputs, checker, new Set(seen)),
    );
  }
  const symbol = expressionSymbol(
    ts.isTypeReferenceNode(node) ? node.typeName : node,
    checker,
  );
  if (!symbol || seen.has(symbol)) return false;
  seen.add(symbol);
  const declarations = symbol.getDeclarations() ?? [];
  if (
    declarations.length > 0 &&
    declarations.every((declaration) =>
      generatedOutputs.has(path.resolve(declaration.getSourceFile().fileName)),
    )
  ) {
    return true;
  }
  return declarations.some(
    (declaration) =>
      ts.isTypeAliasDeclaration(declaration) &&
      hasGeneratedTypeNode(
        declaration.type,
        generatedOutputs,
        checker,
        new Set(seen),
      ),
  );
}

function isGlobalFetchSymbol(symbol) {
  if (!symbol || symbol.getName() !== "fetch") return false;
  const declarations = symbol.getDeclarations() ?? [];
  return declarations.some((declaration) =>
    /(^|\/)lib\.[^/]+\.d\.ts$/.test(
      normalizePath(declaration.getSourceFile().fileName),
    ),
  );
}

function isJSONStringifyCall(node, checker) {
  if (
    !ts.isCallExpression(node) ||
    !ts.isPropertyAccessExpression(node.expression) ||
    node.expression.name.text !== "stringify"
  ) {
    return false;
  }
  const owner = node.expression.expression;
  if (!ts.isIdentifier(owner) || owner.text !== "JSON") return false;
  const symbol = checker.getSymbolAtLocation(owner);
  if (!symbol) return false;
  const declarations = symbol.getDeclarations() ?? [];
  return (
    declarations.length > 0 &&
    declarations.every((declaration) =>
      /(^|\/)lib\.[^/]+\.d\.ts$/.test(
        normalizePath(declaration.getSourceFile().fileName),
      ),
    )
  );
}

function isConstVariableDeclaration(declaration) {
  const declarationList = declaration.parent;
  return (
    ts.isVariableDeclarationList(declarationList) &&
    (declarationList.flags & ts.NodeFlags.Const) !== 0
  );
}

function declarationSymbolName(declaration) {
  return declaration.name && ts.isIdentifier(declaration.name)
    ? declaration.name.text
    : "";
}

function propertyName(name) {
  if (ts.isIdentifier(name) || ts.isStringLiteralLike(name)) return name.text;
  if (
    ts.isComputedPropertyName(name) &&
    ts.isStringLiteralLike(name.expression)
  ) {
    return name.expression.text;
  }
  return undefined;
}

function isAbsent(expression) {
  expression = unwrapExpression(expression);
  return (
    expression.kind === ts.SyntaxKind.UndefinedKeyword ||
    expression.kind === ts.SyntaxKind.NullKeyword ||
    (ts.isIdentifier(expression) && expression.text === "undefined")
  );
}

function unwrapExpression(expression) {
  while (
    ts.isParenthesizedExpression(expression) ||
    ts.isAsExpression(expression) ||
    ts.isSatisfiesExpression(expression) ||
    ts.isTypeAssertionExpression(expression) ||
    ts.isNonNullExpression(expression)
  ) {
    expression = expression.expression;
  }
  return expression;
}

function unwrapParentheses(expression) {
  while (ts.isParenthesizedExpression(expression)) {
    expression = expression.expression;
  }
  return expression;
}

function knownBodies() {
  return { bodies: [], unknown: false };
}

function unknownBodies() {
  return { bodies: [], unknown: true };
}

function mergeBodies(target, source) {
  target.bodies.push(...source.bodies);
  target.unknown ||= source.unknown;
  return target;
}

function unknownPayloads() {
  return { absent: false, payloads: [], unknown: true };
}

function mergePayloads(target, source) {
  target.absent &&= source.absent;
  target.payloads.push(...source.payloads);
  target.unknown ||= source.unknown;
  return target;
}

function readTygoOutputPaths(root) {
  const manifestPath = path.join(root, "tygo.yaml");
  let manifest;
  try {
    manifest = readFileSync(manifestPath, "utf8");
  } catch (error) {
    throw new Error(`could not read ${manifestPath}: ${error.message}`, {
      cause: error,
    });
  }

  const outputs = new Set();
  for (const [index, line] of manifest.split(/\r?\n/u).entries()) {
    if (!/^\s*(?:-\s*)?output_path\s*:/u.test(line)) continue;
    const match = line.match(
      /^\s*(?:-\s*)?output_path\s*:\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^']|'')*)'|([^#]*?))\s*(?:#.*)?$/u,
    );
    if (!match) {
      throw new Error(
        `could not parse output_path in ${manifestPath}:${index + 1}`,
      );
    }

    let outputPath;
    if (match[1] !== undefined) {
      try {
        outputPath = JSON.parse(`"${match[1]}"`);
      } catch {
        throw new Error(
          `could not parse output_path in ${manifestPath}:${index + 1}`,
        );
      }
    } else if (match[2] !== undefined) {
      outputPath = match[2].replaceAll("''", "'");
    } else {
      outputPath = match[3].trim();
    }
    if (!outputPath) {
      throw new Error(`empty output_path in ${manifestPath}:${index + 1}`);
    }
    outputs.add(path.resolve(root, outputPath));
  }
  return outputs;
}

function findConfig(root) {
  for (const name of ["tsconfig.app.json", "tsconfig.json"]) {
    const candidate = path.join(root, name);
    if (ts.sys.fileExists(candidate)) return candidate;
  }
  throw new Error(`no TypeScript config found under ${root}`);
}

function isProductionAppSource(sourceFile, appRoot, included) {
  const filename = path.resolve(sourceFile.fileName);
  const relative = normalizePath(path.relative(appRoot, filename));
  if (relative.startsWith("../") || relative === "..") return false;
  if (sourceFile.isDeclarationFile) return false;
  if (/(^|\/)(__tests__|test|tests)(\/|$)/.test(relative)) return false;
  if (/\.(test|spec)\.[cm]?[jt]sx?$/.test(relative)) return false;
  return (
    included === undefined ||
    included.has(normalizePath(path.join("app", relative)))
  );
}

function normalizePath(filename) {
  return filename.split(path.sep).join("/");
}

function formatCompilerDiagnostic(diagnostic) {
  return ts.flattenDiagnosticMessageText(diagnostic.messageText, "\n");
}

function runCLI() {
  const diagnostics = checkTypeScriptContracts(process.cwd());
  if (diagnostics.length === 0) return;
  process.stderr.write(`${diagnostics.join("\n")}\n`);
  process.exitCode = 1;
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
  runCLI();
}
