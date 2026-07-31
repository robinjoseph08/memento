import path from "node:path";
import process from "node:process";
import { pathToFileURL } from "node:url";

import ts from "typescript";

const sharedAPIFunctions = new Set(["apiJSON", "apiNoContent", "apiResponse"]);
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
  const context = createAnalysisContext({
    apiPath,
    checker,
    generatedRoot,
    sourceFiles,
  });
  const diagnostics = new Set();

  for (const sourceFile of sourceFiles) {
    const sourcePath = path.resolve(sourceFile.fileName);
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
      if (ts.isCallExpression(node) && sourcePath !== apiPath) {
        const target = resolveCallTarget(node.expression, context, new Set());
        if (target.sharedNames.has("apiJSON")) {
          checkResponseContract(node, report, context);
        }
        if (target.sharedNames.size > 0 && node.arguments.length >= 2) {
          checkRequestContract(node.arguments[1], report, context);
        }
        if (target.globalFetch) {
          checkDirectFetch(node, report, context);
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sourceFile);
  }

  return [...diagnostics].sort();
}

function createAnalysisContext({
  apiPath,
  checker,
  generatedRoot,
  sourceFiles,
}) {
  const context = {
    apiPath,
    callSites: new Map(),
    checker,
    generatedRoot,
    sourceFiles,
  };

  for (const sourceFile of sourceFiles) {
    const visit = (node) => {
      if (ts.isCallExpression(node)) {
        const target = resolveCallTarget(node.expression, context, new Set());
        for (const fn of target.functions) {
          const calls = context.callSites.get(fn) ?? [];
          calls.push(node);
          context.callSites.set(fn, calls);
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sourceFile);
  }
  return context;
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
  if (!hasGeneratedResponseType(responseType, call, context, new Set())) {
    report(
      responseTypeNode,
      "response-contract",
      `apiJSON response type ${context.checker.typeToString(responseType)} must be declared in app/types/generated`,
    );
  }
}

function hasGeneratedResponseType(type, call, context, seen) {
  if (hasGeneratedDeclaration(type, context.generatedRoot, context.checker)) {
    return true;
  }
  if ((type.flags & ts.TypeFlags.TypeParameter) === 0) return false;

  const symbol = type.getSymbol();
  if (!symbol || seen.has(symbol)) return false;
  seen.add(symbol);
  const declaration = (symbol.getDeclarations() ?? []).find(
    ts.isTypeParameterDeclaration,
  );
  const fn = declaration && enclosingFunction(declaration);
  if (!declaration || !fn || !fn.typeParameters) return false;
  const typeParameterIndex = fn.typeParameters.indexOf(declaration);
  if (typeParameterIndex < 0) return false;

  const callSites = context.callSites.get(fn) ?? [];
  if (callSites.length === 0) return false;
  return callSites.every((outerCall) => {
    let instantiatedType;
    const typeArgument = outerCall.typeArguments?.[typeParameterIndex];
    if (typeArgument) {
      instantiatedType = context.checker.getTypeFromTypeNode(typeArgument);
    } else if (isDirectFunctionReturn(call, fn)) {
      instantiatedType = promisedType(
        context.checker.getTypeAtLocation(outerCall),
        context.checker,
      );
    }
    return (
      instantiatedType !== undefined &&
      hasGeneratedResponseType(
        instantiatedType,
        outerCall,
        context,
        new Set(seen),
      )
    );
  });
}

function isDirectFunctionReturn(call, fn) {
  if (ts.isArrowFunction(fn) && fn.body === call) return true;
  return (
    ts.isReturnStatement(call.parent) &&
    call.parent.expression === call &&
    enclosingFunction(call.parent) === fn
  );
}

function promisedType(type, checker) {
  return checker.getPromisedTypeOfPromise(type) ?? type;
}

function checkRequestContract(init, report, context) {
  const options = requestBodies(init, new Map(), context, new Set(), 0);
  if (options.unknown) {
    report(
      init,
      "request-contract",
      "shared API request options could not be resolved deterministically",
    );
  }

  for (const body of options.bodies) {
    const serialized = serializedPayloads(
      body.expression,
      body.environment,
      context,
      new Set(),
      0,
    );
    if (serialized.unknown) {
      report(
        body.expression,
        "request-contract",
        "shared API request body could not be resolved deterministically",
      );
      continue;
    }
    if (serialized.payloads.length === 0 && !serialized.absent) {
      report(
        body.expression,
        "request-contract",
        "shared API request body must serialize a payload with generated-type provenance",
      );
    }
    for (const payload of serialized.payloads) {
      if (
        !hasGeneratedProvenance(
          payload.expression,
          payload.environment,
          context,
          new Set(),
          0,
        )
      ) {
        report(
          payload.expression,
          "request-contract",
          "shared API request payload must have generated-type provenance",
        );
      }
    }
  }
}

function requestBodies(expression, environment, context, seen, depth) {
  if (depth > maximumAnalysisDepth) return unknownBodies();
  expression = unwrapExpression(expression);
  if (isAbsentBody(expression)) return knownBodies();

  if (ts.isObjectLiteralExpression(expression)) {
    const result = knownBodies();
    for (const property of expression.properties) {
      if (
        ts.isPropertyAssignment(property) &&
        propertyName(property.name) === "body"
      ) {
        result.bodies.push({
          environment,
          expression: property.initializer,
        });
      } else if (
        ts.isShorthandPropertyAssignment(property) &&
        property.name.text === "body"
      ) {
        result.bodies.push({ environment, expression: property.name });
      } else if (ts.isSpreadAssignment(property)) {
        mergeBodies(
          result,
          requestBodies(
            property.expression,
            environment,
            context,
            seen,
            depth + 1,
          ),
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
      requestBodies(
        expression.whenTrue,
        environment,
        context,
        new Set(seen),
        depth + 1,
      ),
      requestBodies(
        expression.whenFalse,
        environment,
        context,
        new Set(seen),
        depth + 1,
      ),
    );
  }

  const references = referencedValues(expression, environment, context, seen);
  if (references.length > 0) {
    const result = knownBodies();
    for (const reference of references) {
      mergeBodies(
        result,
        requestBodies(
          reference.expression,
          reference.environment,
          context,
          new Set(seen),
          depth + 1,
        ),
      );
    }
    return result;
  }

  if (ts.isCallExpression(expression)) {
    return requestBodiesFromFunctionCall(
      expression,
      environment,
      context,
      seen,
      depth,
    );
  }

  return unknownBodies();
}

function requestBodiesFromFunctionCall(
  call,
  environment,
  context,
  seen,
  depth,
) {
  const target = resolveCallTarget(call.expression, context, new Set());
  if (target.functions.length === 0) return unknownBodies();
  const result = knownBodies();
  for (const fn of target.functions) {
    const returns = functionReturns(fn);
    if (returns.length === 0) {
      result.unknown = true;
      continue;
    }
    const callEnvironment = bindArguments(fn, call, environment, context);
    for (const returned of returns) {
      mergeBodies(
        result,
        requestBodies(
          returned,
          callEnvironment,
          context,
          new Set(seen),
          depth + 1,
        ),
      );
    }
  }
  return result;
}

function serializedPayloads(expression, environment, context, seen, depth) {
  if (depth > maximumAnalysisDepth) return unknownPayloads();
  expression = unwrapExpression(expression);
  if (isAbsentBody(expression)) {
    return { absent: true, payloads: [], unknown: false };
  }
  if (isJSONStringifyCall(expression, context.checker)) {
    return {
      absent: false,
      payloads:
        expression.arguments.length > 0
          ? [{ environment, expression: expression.arguments[0] }]
          : [],
      unknown: expression.arguments.length === 0,
    };
  }
  if (ts.isConditionalExpression(expression)) {
    return mergePayloads(
      serializedPayloads(
        expression.whenTrue,
        environment,
        context,
        new Set(seen),
        depth + 1,
      ),
      serializedPayloads(
        expression.whenFalse,
        environment,
        context,
        new Set(seen),
        depth + 1,
      ),
    );
  }

  const references = referencedValues(expression, environment, context, seen);
  if (references.length > 0) {
    const result = { absent: true, payloads: [], unknown: false };
    for (const reference of references) {
      mergePayloads(
        result,
        serializedPayloads(
          reference.expression,
          reference.environment,
          context,
          new Set(seen),
          depth + 1,
        ),
      );
    }
    return result;
  }

  if (ts.isCallExpression(expression)) {
    const target = resolveCallTarget(expression.expression, context, new Set());
    if (target.functions.length === 0) return unknownPayloads();
    const result = { absent: true, payloads: [], unknown: false };
    for (const fn of target.functions) {
      const returns = functionReturns(fn);
      if (returns.length === 0) {
        result.unknown = true;
        continue;
      }
      const callEnvironment = bindArguments(
        fn,
        expression,
        environment,
        context,
      );
      for (const returned of returns) {
        mergePayloads(
          result,
          serializedPayloads(
            returned,
            callEnvironment,
            context,
            new Set(seen),
            depth + 1,
          ),
        );
      }
    }
    return result;
  }

  return { absent: false, payloads: [], unknown: false };
}

function hasGeneratedProvenance(expression, environment, context, seen, depth) {
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

  const symbol = expressionSymbol(expression, context.checker);
  const bound = symbol && environment.get(symbol);
  if (bound) {
    return hasGeneratedProvenance(
      bound.expression,
      bound.environment,
      context,
      new Set(seen),
      depth + 1,
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
        hasGeneratedProvenance(
          value,
          environment,
          context,
          new Set(seen),
          depth + 1,
        )
      ) {
        return true;
      }
    }
  }

  if (ts.isCallExpression(expression)) {
    const signature = context.checker.getResolvedSignature(expression);
    if (
      signature &&
      hasGeneratedDeclaration(
        context.checker.getReturnTypeOfSignature(signature),
        context.generatedRoot,
        context.checker,
      )
    ) {
      return true;
    }
    const target = resolveCallTarget(expression.expression, context, new Set());
    return (
      target.functions.length > 0 &&
      target.functions.every((fn) => {
        const returns = functionReturns(fn);
        if (returns.length === 0) return false;
        const callEnvironment = bindArguments(
          fn,
          expression,
          environment,
          context,
        );
        return returns.every((returned) =>
          hasGeneratedProvenance(
            returned,
            callEnvironment,
            context,
            new Set(seen),
            depth + 1,
          ),
        );
      })
    );
  }

  return false;
}

function checkDirectFetch(call, report, context) {
  if (call.arguments.length === 0) {
    report(
      call.expression,
      "direct-api-fetch",
      "fetch URL could not be resolved deterministically; possible /api traffic must use app/api.ts",
    );
    return;
  }
  const paths = constantStrings(
    call.arguments[0],
    new Map(),
    context,
    new Set(),
    0,
  );
  if (
    paths.values.some(isAPIPathText) ||
    paths.prefixes.some(isDefinitelyAPIPathPrefix)
  ) {
    report(
      call.expression,
      "direct-api-fetch",
      "direct /api fetch is only allowed in app/api.ts",
    );
  } else if (
    paths.unbounded ||
    paths.prefixes.some((prefix) => !isDefinitelyNonAPIPathPrefix(prefix))
  ) {
    report(
      call.expression,
      "direct-api-fetch",
      "fetch URL could not be resolved deterministically; possible /api traffic must use app/api.ts",
    );
  }
}

function constantStrings(expression, environment, context, seen, depth) {
  if (depth > maximumAnalysisDepth) return unknownStrings();
  expression = unwrapExpression(expression);
  if (ts.isStringLiteralLike(expression)) {
    return { prefixes: [], unbounded: false, values: [expression.text] };
  }
  if (ts.isTemplateExpression(expression)) {
    let result = {
      prefixes: [],
      unbounded: false,
      values: [expression.head.text],
    };
    for (const span of expression.templateSpans) {
      const part = constantStrings(
        span.expression,
        environment,
        context,
        new Set(seen),
        depth + 1,
      );
      result = concatenateStrings(result, part, span.literal.text);
    }
    return result;
  }
  if (
    ts.isBinaryExpression(expression) &&
    expression.operatorToken.kind === ts.SyntaxKind.PlusToken
  ) {
    return concatenateStrings(
      constantStrings(
        expression.left,
        environment,
        context,
        new Set(seen),
        depth + 1,
      ),
      constantStrings(
        expression.right,
        environment,
        context,
        new Set(seen),
        depth + 1,
      ),
      "",
    );
  }
  if (ts.isConditionalExpression(expression)) {
    return mergeStrings(
      constantStrings(
        expression.whenTrue,
        environment,
        context,
        new Set(seen),
        depth + 1,
      ),
      constantStrings(
        expression.whenFalse,
        environment,
        context,
        new Set(seen),
        depth + 1,
      ),
    );
  }

  const references = referencedValues(expression, environment, context, seen, {
    constantsOnly: true,
  });
  if (references.length > 0) {
    const result = { prefixes: [], unbounded: false, values: [] };
    for (const reference of references) {
      mergeStrings(
        result,
        constantStrings(
          reference.expression,
          reference.environment,
          context,
          new Set(seen),
          depth + 1,
        ),
      );
    }
    return result;
  }
  return unknownStrings();
}

function referencedValues(
  expression,
  environment,
  context,
  seen,
  options = {},
) {
  if (
    !ts.isIdentifier(expression) &&
    !ts.isPropertyAccessExpression(expression)
  ) {
    return [];
  }
  const symbol = expressionSymbol(expression, context.checker);
  if (!symbol || seen.has(symbol)) return [];
  seen.add(symbol);

  const bound = environment.get(symbol);
  if (bound) return [bound];

  const result = [];
  for (const declaration of symbol.getDeclarations() ?? []) {
    const value = declarationValue(declaration);
    if (
      value &&
      (!options.constantsOnly || isConstantDeclaration(declaration))
    ) {
      result.push({ environment, expression: value });
      continue;
    }
    if (ts.isParameter(declaration)) {
      const fn = enclosingFunction(declaration);
      if (!fn) continue;
      for (const call of context.callSites.get(fn) ?? []) {
        const index = fn.parameters.indexOf(declaration);
        const argument = call.arguments[index];
        if (argument) {
          result.push({
            environment: bindArguments(fn, call, new Map(), context),
            expression: argument,
          });
        }
      }
    }
  }
  return result;
}

function resolveCallTarget(expression, context, seen) {
  expression = unwrapExpression(expression);
  const result = emptyCallTarget();

  if (ts.isConditionalExpression(expression)) {
    mergeCallTarget(
      result,
      resolveCallTarget(expression.whenTrue, context, new Set(seen)),
    );
    mergeCallTarget(
      result,
      resolveCallTarget(expression.whenFalse, context, new Set(seen)),
    );
    return result;
  }
  if (
    ts.isBinaryExpression(expression) &&
    [ts.SyntaxKind.BarBarToken, ts.SyntaxKind.QuestionQuestionToken].includes(
      expression.operatorToken.kind,
    )
  ) {
    mergeCallTarget(
      result,
      resolveCallTarget(expression.left, context, new Set(seen)),
    );
    mergeCallTarget(
      result,
      resolveCallTarget(expression.right, context, new Set(seen)),
    );
    return result;
  }
  if (ts.isCallExpression(expression)) {
    const factory = resolveCallTarget(
      expression.expression,
      context,
      new Set(),
    );
    for (const fn of factory.functions) {
      for (const returned of functionReturns(fn)) {
        mergeCallTarget(
          result,
          resolveCallTarget(returned, context, new Set(seen)),
        );
      }
    }
    return result;
  }

  const symbol = expressionSymbol(expression, context.checker);
  if (!symbol || seen.has(symbol)) return result;
  seen.add(symbol);

  const declarations = symbol.getDeclarations() ?? [];
  if (
    declarations.some(
      (declaration) =>
        path.resolve(declaration.getSourceFile().fileName) ===
          context.apiPath &&
        sharedAPIFunctions.has(declarationSymbolName(declaration)),
    )
  ) {
    for (const declaration of declarations) {
      const name = declarationSymbolName(declaration);
      if (
        path.resolve(declaration.getSourceFile().fileName) ===
          context.apiPath &&
        sharedAPIFunctions.has(name)
      ) {
        result.sharedNames.add(name);
      }
    }
    return result;
  }
  if (isGlobalFetchSymbol(symbol)) {
    result.globalFetch = true;
    return result;
  }

  for (const declaration of declarations) {
    if (isFunctionLikeDeclaration(declaration)) {
      result.functions.push(declaration);
      continue;
    }
    if (ts.isParameter(declaration)) {
      const fn = enclosingFunction(declaration);
      const index = fn?.parameters.indexOf(declaration) ?? -1;
      for (const call of (fn && context.callSites.get(fn)) ?? []) {
        const argument = call.arguments[index];
        if (argument) {
          mergeCallTarget(
            result,
            resolveCallTarget(argument, context, new Set(seen)),
          );
        }
      }
      if (declaration.type) {
        mergeCallTarget(
          result,
          resolveTypeNodeCallTarget(declaration.type, context, new Set(seen)),
        );
      }
      continue;
    }
    const value = declarationValue(declaration);
    if (!value) continue;
    if (isFunctionLikeDeclaration(value)) {
      result.functions.push(value);
    } else {
      mergeCallTarget(result, resolveCallTarget(value, context, new Set(seen)));
    }
  }
  result.functions = [...new Set(result.functions)];
  return result;
}

function resolveTypeNodeCallTarget(node, context, seen) {
  node = ts.isParenthesizedTypeNode(node) ? node.type : node;
  if (ts.isTypeQueryNode(node)) {
    return resolveCallTarget(node.exprName, context, seen);
  }
  if (ts.isUnionTypeNode(node) || ts.isIntersectionTypeNode(node)) {
    const result = emptyCallTarget();
    for (const part of node.types) {
      mergeCallTarget(
        result,
        resolveTypeNodeCallTarget(part, context, new Set(seen)),
      );
    }
    return result;
  }
  return emptyCallTarget();
}

function expressionSymbol(expression, checker) {
  let symbol = checker.getSymbolAtLocation(expression);
  if (!symbol && ts.isPropertyAccessExpression(expression)) {
    symbol = checker.getSymbolAtLocation(expression.name);
  }
  if (symbol && (symbol.flags & ts.SymbolFlags.Alias) !== 0) {
    symbol = checker.getAliasedSymbol(symbol);
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

function isConstantDeclaration(declaration) {
  if (!ts.isVariableDeclaration(declaration)) return true;
  const declarationList = declaration.parent;
  return (
    ts.isVariableDeclarationList(declarationList) &&
    (declarationList.flags & ts.NodeFlags.Const) !== 0
  );
}

function bindArguments(fn, call, callerEnvironment, context) {
  const environment = new Map();
  for (let index = 0; index < fn.parameters.length; index += 1) {
    const parameter = fn.parameters[index];
    const symbol = context.checker.getSymbolAtLocation(parameter.name);
    const argument = call.arguments[index] ?? parameter.initializer;
    if (symbol && argument) {
      environment.set(symbol, {
        environment: callerEnvironment,
        expression: argument,
      });
    }
  }
  return environment;
}

function functionReturns(fn) {
  if (ts.isArrowFunction(fn) && !ts.isBlock(fn.body)) return [fn.body];
  if (!fn.body || !ts.isBlock(fn.body)) return [];
  const returns = [];
  const visit = (node) => {
    if (node !== fn && isFunctionLikeDeclaration(node)) return;
    if (ts.isReturnStatement(node) && node.expression) {
      returns.push(node.expression);
      return;
    }
    ts.forEachChild(node, visit);
  };
  visit(fn.body);
  return returns;
}

function enclosingFunction(node) {
  for (let current = node.parent; current; current = current.parent) {
    if (isFunctionLikeDeclaration(current)) return current;
  }
  return undefined;
}

function isFunctionLikeDeclaration(node) {
  return (
    ts.isFunctionDeclaration(node) ||
    ts.isFunctionExpression(node) ||
    ts.isArrowFunction(node) ||
    ts.isMethodDeclaration(node) ||
    ts.isGetAccessorDeclaration(node) ||
    ts.isSetAccessorDeclaration(node)
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
  return symbol ? isLibrarySymbol(symbol) : false;
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
  return symbol.getName() === "fetch" && isLibrarySymbol(symbol);
}

function isLibrarySymbol(symbol) {
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

function declarationSymbolName(declaration) {
  return declaration.name && ts.isIdentifier(declaration.name)
    ? declaration.name.text
    : "";
}

function propertyName(name) {
  if (ts.isIdentifier(name) || ts.isStringLiteralLike(name)) return name.text;
  return undefined;
}

function isAPIPathText(text) {
  return (
    text === "/api" || text.startsWith("/api/") || text.startsWith("/api?")
  );
}

function isDefinitelyAPIPathPrefix(prefix) {
  return prefix.startsWith("/api/") || prefix.startsWith("/api?");
}

function isDefinitelyNonAPIPathPrefix(prefix) {
  const apiStem = "/api";
  if (prefix.length < apiStem.length) return !apiStem.startsWith(prefix);
  if (!prefix.startsWith(apiStem)) return true;
  return prefix.length > apiStem.length && !["/", "?"].includes(prefix[4]);
}

function isAbsentBody(expression) {
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

function emptyCallTarget() {
  return { functions: [], globalFetch: false, sharedNames: new Set() };
}

function mergeCallTarget(target, source) {
  target.functions.push(...source.functions);
  target.globalFetch ||= source.globalFetch;
  for (const name of source.sharedNames) target.sharedNames.add(name);
  return target;
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

function unknownStrings() {
  return { prefixes: [], unbounded: true, values: [] };
}

function mergeStrings(target, source) {
  target.values.push(...source.values);
  target.values = [...new Set(target.values)];
  target.prefixes.push(...source.prefixes);
  target.prefixes = [...new Set(target.prefixes)];
  target.unbounded ||= source.unbounded;
  return target;
}

function concatenateStrings(left, right, suffix) {
  const values = [];
  const prefixes = [];
  for (const leftValue of left.values) {
    for (const rightValue of right.values) {
      values.push(leftValue + rightValue + suffix);
    }
    for (const rightPrefix of right.prefixes) {
      prefixes.push(leftValue + rightPrefix);
    }
    if (right.unbounded) prefixes.push(leftValue);
  }
  prefixes.push(...left.prefixes);
  return {
    prefixes: [...new Set(prefixes)],
    unbounded: left.unbounded,
    values: [...new Set(values)],
  };
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
