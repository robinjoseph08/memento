import { createHash } from "node:crypto";
import { createServer, type Server } from "node:http";
import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { extname, join, resolve, sep } from "node:path";

import { build, mergeConfig, type Plugin } from "vite";

import mementoConfig from "../../../vite.config";

type RevisionEvidence = {
  graphPaths: string[];
  workerDigest: string;
  workerRevision: string;
};

type ProductionRevisions = {
  activateSecond(): void;
  close(): Promise<void>;
  first: RevisionEvidence;
  origin: string;
  second: RevisionEvidence;
};

const contentTypes = new Map([
  [".css", "text/css"],
  [".html", "text/html"],
  [".ico", "image/x-icon"],
  [".js", "text/javascript"],
  [".json", "application/json"],
  [".png", "image/png"],
  [".svg", "image/svg+xml"],
  [".webmanifest", "application/manifest+json"],
  [".woff", "font/woff"],
  [".woff2", "font/woff2"],
]);

const secondRevisionGraph: Plugin = {
  name: "memento-second-production-revision",
  renderChunk(code, chunk) {
    if (!chunk.isEntry) return null;
    return `${code}\nconsole.info("Memento production revision evidence");`;
  },
};

async function productionBuild(outDir: string, plugins: Plugin[] = []) {
  await build(
    mergeConfig(mementoConfig, {
      build: { emptyOutDir: true, outDir },
      logLevel: "error",
      plugins,
    }),
  );
}

async function revisionEvidence(root: string): Promise<RevisionEvidence> {
  const [index, worker] = await Promise.all([
    readFile(join(root, "index.html"), "utf8"),
    readFile(join(root, "service-worker.js"), "utf8"),
  ]);
  const workerRevision = /const BUILD_REVISION = "([a-f0-9]+)";/.exec(
    worker,
  )?.[1];
  if (!workerRevision || worker.includes("__MEMENTO_BUILD_REVISION__")) {
    throw new Error("production service worker is missing its build revision");
  }
  return {
    graphPaths: [
      ...new Set(index.match(/\/assets\/[A-Za-z0-9._/-]+/g) ?? []),
    ].sort(),
    workerDigest: createHash("sha256").update(worker).digest("hex"),
    workerRevision,
  };
}

function listen(server: Server) {
  return new Promise<number>((resolvePort, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      server.off("error", reject);
      const address = server.address();
      if (!address || typeof address === "string") {
        reject(new Error("production revision server did not bind a TCP port"));
        return;
      }
      resolvePort(address.port);
    });
  });
}

export async function startProductionRevisions(): Promise<ProductionRevisions> {
  const temporaryRoot = await mkdtemp(
    join(tmpdir(), "memento-production-revisions-"),
  );
  const firstRoot = join(temporaryRoot, "first");
  const secondRoot = join(temporaryRoot, "second");

  try {
    await productionBuild(firstRoot);
    await productionBuild(secondRoot, [secondRevisionGraph]);
    const [first, second] = await Promise.all([
      revisionEvidence(firstRoot),
      revisionEvidence(secondRoot),
    ]);
    if (
      first.workerDigest === second.workerDigest ||
      first.workerRevision === second.workerRevision ||
      first.graphPaths.join("\0") === second.graphPaths.join("\0")
    ) {
      throw new Error(
        "production graph change did not produce distinct worker and asset revisions",
      );
    }

    let activeRoot = firstRoot;
    const server = createServer((request, response) => {
      void (async () => {
        try {
          const requestURL = new URL(request.url ?? "/", "http://127.0.0.1");
          const pathname = decodeURIComponent(requestURL.pathname);
          const requestedPath = pathname === "/" ? "/index.html" : pathname;
          let filePath = resolve(activeRoot, `.${requestedPath}`);
          if (!filePath.startsWith(`${activeRoot}${sep}`)) {
            response.writeHead(400).end();
            return;
          }
          try {
            if (!(await stat(filePath)).isFile()) throw new Error("not a file");
          } catch {
            filePath = join(activeRoot, "index.html");
          }
          const body = await readFile(filePath);
          response.setHeader(
            "Content-Type",
            contentTypes.get(extname(filePath)) ?? "application/octet-stream",
          );
          response.setHeader("Cache-Control", "no-cache");
          if (pathname === "/service-worker.js") {
            response.setHeader("Service-Worker-Allowed", "/");
          }
          response.writeHead(200);
          response.end(body);
        } catch (error) {
          response.writeHead(500, { "Content-Type": "text/plain" });
          response.end(error instanceof Error ? error.message : String(error));
        }
      })();
    });
    const port = await listen(server);

    return {
      activateSecond() {
        activeRoot = secondRoot;
      },
      async close() {
        await new Promise<void>((resolveClose, reject) => {
          server.close((error) => (error ? reject(error) : resolveClose()));
        });
        await rm(temporaryRoot, { force: true, recursive: true });
      },
      first,
      origin: `http://127.0.0.1:${port}`,
      second,
    };
  } catch (error) {
    await rm(temporaryRoot, { force: true, recursive: true });
    throw error;
  }
}
