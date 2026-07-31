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
  const generatedRoot = path.join(appRoot, "types", "generated");
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
    generatedRoot,
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
    if (sourcePath === apiPath) continue;
    const relative = normalizePath(path.relative(root, sourcePath));
    const report = (node, code, message) => {
      const start = sourceFile.getLineAndCharacterOfPosition(
        node.getStart(sourceFile),
      );
      diagnostics.add(
        `${relative}:${start.line + 1}:${start.character + 1} [${code}]: ${message}`,
      );
    };

    const visit = (node) => {
      if (ts.isCallExpression(node)) {
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

      if (isReferenceExpression(node) && !isNestedReferenceName(node)) {
        const sharedName = sharedAPINameForExpression(node, context);
        if (
          sharedName &&
          !isImportReference(node) &&
          (!isDirectCallTarget(node) || referenceName(node) !== sharedName)
        ) {
          report(
            node,
            "shared-api-indirection",
            `${sharedName} must be called directly and cannot be stored, passed, returned, rebound, or wrapped`,
          );
        }
        if (isGlobalFetchExpression(node, checker)) {
          report(
            node,
            "direct-fetch",
            "global fetch is only allowed in app/api.ts",
          );
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
            "direct-fetch",
            "global fetch is only allowed in app/api.ts",
          );
        }
      }

      ts.forEachChild(node, visit);
    };
    visit(sourceFile);
  }

  return [...diagnostics].sort();
}

function checkResponseContract(call, report, context) {
  if (call.typeArguments?.length !== 1) {
    report(
      call.expression,
      "response-contract",
      "apiJSON must declare one response type from app/types/generated",
    );
    return;
  }

  const responseTypeNode = call.typeArguments[0];
  const responseType = context.checker.getTypeFromTypeNode(responseTypeNode);
  if (
    !hasGeneratedDeclaration(
      responseType,
      context.generatedRoot,
      context.checker,
    )
  ) {
    report(
      responseTypeNode,
      "response-contract",
      `apiJSON response type ${context.checker.typeToString(responseType)} must be declared in app/types/generated`,
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
      context.generatedRoot,
      context.checker,
    );
  }

  if (
    hasGeneratedDeclaration(
      context.checker.getTypeAtLocation(expression),
      context.generatedRoot,
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
          context.generatedRoot,
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
        context.generatedRoot,
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

function directSharedAPIName(expression, context) {
  expression = unwrapExpression(expression);
  return sharedAPINameForExpression(expression, context);
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

function isGlobalFetchExpression(expression, checker) {
  return isGlobalFetchSymbol(expressionSymbol(expression, checker));
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
  generatedRoot,
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
        hasGeneratedDeclaration(part, generatedRoot, checker, new Set(seen)),
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
      isWithin(declaration.getSourceFile().fileName, generatedRoot),
    )
  ) {
    return true;
  }
  return declarations.some(
    (declaration) =>
      ts.isTypeAliasDeclaration(declaration) &&
      hasGeneratedTypeNode(
        declaration.type,
        generatedRoot,
        checker,
        new Set(seen),
      ),
  );
}

function hasGeneratedTypeNode(node, generatedRoot, checker, seen) {
  if (ts.isUnionTypeNode(node) || ts.isIntersectionTypeNode(node)) {
    return node.types.every((part) =>
      hasGeneratedTypeNode(part, generatedRoot, checker, new Set(seen)),
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
      isWithin(declaration.getSourceFile().fileName, generatedRoot),
    )
  ) {
    return true;
  }
  return declarations.some(
    (declaration) =>
      ts.isTypeAliasDeclaration(declaration) &&
      hasGeneratedTypeNode(
        declaration.type,
        generatedRoot,
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

function isWithin(filename, directory) {
  const relative = path.relative(
    path.resolve(directory),
    path.resolve(filename),
  );
  return (
    relative !== ".." &&
    !relative.startsWith(`..${path.sep}`) &&
    !path.isAbsolute(relative)
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
