import path from "node:path";
import process from "node:process";
import { pathToFileURL } from "node:url";

import ts from "typescript";

const sharedAPIFunctions = new Set(["apiJSON", "apiNoContent", "apiResponse"]);

export function checkTypeScriptContracts(repositoryRoot, options = {}) {
  const root = path.resolve(repositoryRoot);
  const appRoot = path.join(root, "app");
  const apiPath = path.join(appRoot, "api.ts");
  const generatedRoot = path.join(appRoot, "types", "generated");
  const configPath = findConfig(root);
  const config = ts.readConfigFile(configPath, ts.sys.readFile);
  if (config.error) {
    throw new Error(formatCompilerDiagnostic(config.error));
  }
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
  const diagnostics = [];

  for (const sourceFile of program.getSourceFiles()) {
    if (!isProductionAppSource(sourceFile, appRoot, included)) continue;
    const sourcePath = path.resolve(sourceFile.fileName);
    const relative = normalizePath(path.relative(root, sourcePath));

    const report = (node, code, message) => {
      const start = sourceFile.getLineAndCharacterOfPosition(
        node.getStart(sourceFile),
      );
      diagnostics.push(
        `${relative}:${start.line + 1}:${start.character + 1} [${code}]: ${message}`,
      );
    };

    const visit = (node) => {
      if (ts.isCallExpression(node)) {
        const sharedName = sharedAPICallName(node, checker, apiPath);
        if (sharedName === "apiJSON") {
          if (node.typeArguments?.length !== 1) {
            report(
              node.expression,
              "response-contract",
              "apiJSON must declare one response type from app/types/generated",
            );
          } else {
            const responseTypeNode = node.typeArguments[0];
            const responseType = checker.getTypeFromTypeNode(responseTypeNode);
            if (
              !hasGeneratedDeclaration(responseType, generatedRoot, checker)
            ) {
              report(
                responseTypeNode,
                "response-contract",
                `apiJSON response type ${checker.typeToString(responseType)} must be declared in app/types/generated`,
              );
            }
          }
        }
        if (sharedName && node.arguments.length >= 2) {
          for (const body of requestBodies(node.arguments[1], checker)) {
            const payloads = serializedPayloads(body);
            if (payloads.length === 0 && !isAbsentBody(body)) {
              report(
                body,
                "request-contract",
                "shared API request body must serialize a payload with generated-type provenance",
              );
            }
            for (const payload of payloads) {
              if (
                !hasGeneratedProvenance(
                  payload,
                  generatedRoot,
                  checker,
                  new Set(),
                )
              ) {
                report(
                  payload,
                  "request-contract",
                  "shared API request payload must have generated-type provenance",
                );
              }
            }
          }
        }
        if (
          sourcePath !== apiPath &&
          isGlobalFetchCall(node, checker) &&
          isAPIPath(node.arguments[0])
        ) {
          report(
            node.expression,
            "direct-api-fetch",
            "direct /api fetch is only allowed in app/api.ts",
          );
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sourceFile);
  }

  diagnostics.sort();
  return diagnostics;
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

function sharedAPICallName(call, checker, apiPath) {
  const symbol = resolvedSymbol(call.expression, checker);
  if (!symbol || !sharedAPIFunctions.has(symbol.getName())) return undefined;
  const declarations = symbol.getDeclarations() ?? [];
  if (
    declarations.some(
      (declaration) =>
        path.resolve(declaration.getSourceFile().fileName) === apiPath,
    )
  ) {
    return symbol.getName();
  }
  return undefined;
}

function resolvedSymbol(expression, checker) {
  let symbol = checker.getSymbolAtLocation(expression);
  if (!symbol && ts.isPropertyAccessExpression(expression)) {
    symbol = checker.getSymbolAtLocation(expression.name);
  }
  if (symbol && (symbol.flags & ts.SymbolFlags.Alias) !== 0) {
    symbol = checker.getAliasedSymbol(symbol);
  }
  return symbol;
}

function requestBodies(expression, checker, seen = new Set()) {
  expression = unwrapParentheses(expression);
  if (ts.isObjectLiteralExpression(expression)) {
    const result = [];
    for (const property of expression.properties) {
      if (
        ts.isPropertyAssignment(property) &&
        propertyName(property.name) === "body"
      ) {
        result.push(property.initializer);
      } else if (ts.isSpreadAssignment(property)) {
        result.push(...requestBodies(property.expression, checker, seen));
      }
    }
    return result;
  }
  if (ts.isConditionalExpression(expression)) {
    return [
      ...requestBodies(expression.whenTrue, checker, seen),
      ...requestBodies(expression.whenFalse, checker, seen),
    ];
  }
  if (ts.isIdentifier(expression)) {
    const symbol = checker.getSymbolAtLocation(expression);
    if (!symbol || seen.has(symbol)) return [];
    seen.add(symbol);
    const result = [];
    for (const declaration of symbol.getDeclarations() ?? []) {
      if (ts.isVariableDeclaration(declaration) && declaration.initializer) {
        result.push(...requestBodies(declaration.initializer, checker, seen));
      }
    }
    return result;
  }
  return [];
}

function propertyName(name) {
  if (ts.isIdentifier(name) || ts.isStringLiteralLike(name)) return name.text;
  return undefined;
}

function serializedPayloads(body) {
  const result = [];
  const visit = (node) => {
    if (isJSONStringifyCall(node)) {
      if (node.arguments.length > 0) result.push(node.arguments[0]);
      return;
    }
    ts.forEachChild(node, visit);
  };
  visit(body);
  return result;
}

function isJSONStringifyCall(node) {
  return (
    ts.isCallExpression(node) &&
    ts.isPropertyAccessExpression(node.expression) &&
    ts.isIdentifier(node.expression.expression) &&
    node.expression.expression.text === "JSON" &&
    node.expression.name.text === "stringify"
  );
}

function hasGeneratedProvenance(expression, generatedRoot, checker, seen) {
  expression = unwrapParentheses(expression);
  if (ts.isSatisfiesExpression(expression) || ts.isAsExpression(expression)) {
    return hasGeneratedDeclaration(
      checker.getTypeFromTypeNode(expression.type),
      generatedRoot,
      checker,
    );
  }
  if (ts.isTypeAssertionExpression(expression)) {
    return hasGeneratedDeclaration(
      checker.getTypeFromTypeNode(expression.type),
      generatedRoot,
      checker,
    );
  }
  if (ts.isCallExpression(expression)) {
    const signature = checker.getResolvedSignature(expression);
    return (
      signature !== undefined &&
      hasGeneratedDeclaration(
        checker.getReturnTypeOfSignature(signature),
        generatedRoot,
        checker,
      )
    );
  }
  if (
    ts.isIdentifier(expression) ||
    ts.isPropertyAccessExpression(expression)
  ) {
    if (
      hasGeneratedDeclaration(
        checker.getTypeAtLocation(expression),
        generatedRoot,
        checker,
      )
    ) {
      return true;
    }
    const symbol = checker.getSymbolAtLocation(
      ts.isPropertyAccessExpression(expression) ? expression.name : expression,
    );
    if (!symbol || seen.has(symbol)) return false;
    seen.add(symbol);
    for (const declaration of symbol.getDeclarations() ?? []) {
      if (
        (ts.isVariableDeclaration(declaration) ||
          ts.isParameter(declaration) ||
          ts.isPropertyDeclaration(declaration) ||
          ts.isPropertySignature(declaration)) &&
        declaration.type &&
        hasGeneratedDeclaration(
          checker.getTypeFromTypeNode(declaration.type),
          generatedRoot,
          checker,
        )
      ) {
        return true;
      }
      if (
        ts.isVariableDeclaration(declaration) &&
        declaration.initializer &&
        hasGeneratedProvenance(
          declaration.initializer,
          generatedRoot,
          checker,
          seen,
        )
      ) {
        return true;
      }
    }
  }
  return false;
}

function hasGeneratedDeclaration(type, generatedRoot, checker) {
  if (type.isUnion()) {
    const relevant = type.types.filter(
      (part) =>
        (part.flags &
          (ts.TypeFlags.Undefined | ts.TypeFlags.Null | ts.TypeFlags.Never)) ===
        0,
    );
    return (
      relevant.length > 0 &&
      relevant.every((part) =>
        hasGeneratedDeclaration(part, generatedRoot, checker),
      )
    );
  }
  const symbol = type.aliasSymbol ?? type.getSymbol();
  if (!symbol) return false;
  const declarations = symbol.getDeclarations() ?? [];
  return (
    declarations.length > 0 &&
    declarations.every((declaration) =>
      isWithin(declaration.getSourceFile().fileName, generatedRoot),
    )
  );
}

function isGlobalFetchCall(call, checker) {
  let target;
  if (ts.isIdentifier(call.expression) && call.expression.text === "fetch") {
    target = call.expression;
  } else if (
    ts.isPropertyAccessExpression(call.expression) &&
    call.expression.name.text === "fetch" &&
    (isGlobalObject(call.expression.expression) ||
      checker.typeToString(
        checker.getTypeAtLocation(call.expression.expression),
      ) === "Window & typeof globalThis")
  ) {
    target = call.expression.name;
  } else {
    return false;
  }
  const symbol = checker.getSymbolAtLocation(target);
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

function isGlobalObject(expression) {
  return (
    ts.isIdentifier(expression) &&
    (expression.text === "window" || expression.text === "globalThis")
  );
}

function isAPIPath(expression) {
  if (!expression) return false;
  expression = unwrapParentheses(expression);
  let text;
  if (ts.isStringLiteralLike(expression)) text = expression.text;
  else if (ts.isTemplateExpression(expression)) text = expression.head.text;
  else return false;
  return (
    text === "/api" || text.startsWith("/api/") || text.startsWith("/api?")
  );
}

function isAbsentBody(expression) {
  expression = unwrapParentheses(expression);
  if (
    expression.kind === ts.SyntaxKind.UndefinedKeyword ||
    expression.kind === ts.SyntaxKind.NullKeyword
  ) {
    return true;
  }
  if (ts.isIdentifier(expression) && expression.text === "undefined")
    return true;
  if (ts.isConditionalExpression(expression)) {
    return (
      isAbsentBody(expression.whenTrue) && isAbsentBody(expression.whenFalse)
    );
  }
  return false;
}

function unwrapParentheses(expression) {
  while (ts.isParenthesizedExpression(expression))
    expression = expression.expression;
  return expression;
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
