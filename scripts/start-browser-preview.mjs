import { spawn, spawnSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import http from "node:http";
import process, { stdout } from "node:process";
import { clearTimeout, setTimeout as scheduleTimeout } from "node:timers";
import { setTimeout as delay } from "node:timers/promises";
import { URL } from "node:url";

const postgresImage =
  "postgres:17.7-alpine3.23@sha256:bb377b7239d2774ac8cc76f481596ce96c5a6b5e9d141f6d0a0ee371a6e7c0f2";
const containerName = `memento-browser-${randomUUID()}`;
const sessionCredentials = Array.from({ length: 4 }, (_, index) =>
  (0x11 + index).toString(16).padStart(2, "0").repeat(32),
);
let apiURL = "";
let fixtureProcess;
let previewServer;
let stopping = false;
let cleanupPromise;

function command(program, args, options = {}) {
  return new Promise((resolve) => {
    const child = spawn(program, args, {
      stdio: [options.input === undefined ? "ignore" : "pipe", "pipe", "pipe"],
    });
    let output = "";
    child.stdout.on("data", (chunk) => {
      if (output.length < 16_384) output += chunk.toString();
    });
    child.stderr.resume();
    if (options.input !== undefined) child.stdin.end(options.input);
    child.once("error", () => resolve({ code: -1, output: "" }));
    child.once("close", (code) => resolve({ code: code ?? -1, output }));
  });
}

async function requireCommand(program, args, stage, options) {
  const result = await command(program, args, options);
  if (result.code !== 0) throw new Error(stage);
  return result.output.trim();
}

function proxyAPI(request, response, next) {
  const pathname = new URL(request.url ?? "/", "http://browser-preview")
    .pathname;
  if (pathname !== "/api" && !pathname.startsWith("/api/")) {
    next();
    return;
  }
  if (!apiURL) {
    response.writeHead(503).end();
    return;
  }
  const target = new URL(request.url ?? "/", apiURL);
  const upstream = http.request(
    target,
    {
      method: request.method,
      headers: { ...request.headers, host: target.host },
    },
    (upstreamResponse) => {
      response.writeHead(
        upstreamResponse.statusCode ?? 502,
        upstreamResponse.headers,
      );
      upstreamResponse.pipe(response);
    },
  );
  upstream.on("error", () => {
    if (!response.headersSent) response.writeHead(502);
    response.end();
  });
  request.on("aborted", () => upstream.destroy());
  request.pipe(upstream);
}

async function startPostgres() {
  await requireCommand(
    "docker",
    [
      "run",
      "--detach",
      "--name",
      containerName,
      "--env",
      "POSTGRES_DB=memento",
      "--env",
      "POSTGRES_USER=postgres",
      "--env",
      "POSTGRES_PASSWORD=browser-test-only-password",
      "--publish",
      "127.0.0.1::5432",
      "--tmpfs",
      "/var/lib/postgresql/data",
      postgresImage,
    ],
    "start browser PostgreSQL",
  );

  const deadline = Date.now() + 60_000;
  let endpoint = "";
  while (Date.now() < deadline) {
    const port = await command("docker", ["port", containerName, "5432/tcp"]);
    if (port.code === 0) endpoint = port.output.trim().split("\n")[0] ?? "";
    const probe = await command("docker", [
      "exec",
      containerName,
      "psql",
      "--host",
      "127.0.0.1",
      "--username",
      "postgres",
      "--dbname",
      "memento",
      "--command",
      "SELECT 1",
    ]);
    if (endpoint && probe.code === 0) {
      const separator = endpoint.lastIndexOf(":");
      const portNumber = endpoint.slice(separator + 1);
      if (/^\d+$/.test(portNumber)) return portNumber;
    }
    await delay(250);
  }
  throw new Error("browser PostgreSQL readiness timed out");
}

async function startPreview() {
  const { preview } = await import("vite");
  previewServer = await preview({
    plugins: [
      {
        name: "memento-browser-api-proxy",
        configurePreviewServer(server) {
          server.middlewares.use(proxyAPI);
        },
      },
    ],
    preview: {
      host: "127.0.0.1",
      port: 0,
      strictPort: true,
    },
  });
  const address = previewServer.httpServer.address();
  if (!address || typeof address === "string") {
    throw new Error("browser preview did not allocate a TCP port");
  }
  return `http://127.0.0.1:${address.port}`;
}

function startFixture(databaseURL, publicOrigin) {
  return new Promise((resolve, reject) => {
    fixtureProcess = spawn(
      "go",
      ["run", "-tags=integration", "./tests/browserfixture"],
      {
        detached: process.platform !== "win32",
        env: {
          ...process.env,
          MEMENTO_TEST_DATABASE_URL: databaseURL,
          MEMENTO_TEST_BROWSER_PUBLIC_ORIGIN: publicOrigin,
          MEMENTO_TEST_BROWSER_SESSION_CREDENTIALS:
            JSON.stringify(sessionCredentials),
        },
        stdio: ["ignore", "pipe", "pipe"],
      },
    );
    fixtureProcess.stderr.resume();
    let buffered = "";
    let settled = false;
    const timeout = scheduleTimeout(() => {
      if (!settled) {
        settled = true;
        reject(new Error("browser fixture readiness timed out"));
      }
    }, 30_000);
    fixtureProcess.stdout.on("data", (chunk) => {
      if (settled) return;
      buffered += chunk.toString();
      if (buffered.length > 16_384) buffered = buffered.slice(-16_384);
      for (const line of buffered.split("\n")) {
        const match =
          /^MEMENTO_BROWSER_API_URL=(http:\/\/127\.0\.0\.1:\d+)$/.exec(
            line.trim(),
          );
        if (!match) continue;
        settled = true;
        clearTimeout(timeout);
        fixtureProcess.stdout.resume();
        resolve(match[1]);
        return;
      }
    });
    fixtureProcess.once("error", () => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      reject(new Error("browser fixture failed to start"));
    });
    fixtureProcess.once("exit", () => {
      if (settled) {
        if (!stopping) {
          process.stderr.write("Browser fixture stopped unexpectedly.\n");
          void cleanup().then(() => process.exit(1));
        }
        return;
      }
      settled = true;
      clearTimeout(timeout);
      reject(new Error("browser fixture stopped before readiness"));
    });
  });
}

async function stopFixture() {
  if (!fixtureProcess || fixtureProcess.exitCode !== null) return;
  const signalFixture = (signal) => {
    try {
      if (process.platform === "win32") fixtureProcess.kill(signal);
      else process.kill(-fixtureProcess.pid, signal);
    } catch {
      // The fixture already stopped.
    }
  };
  signalFixture("SIGTERM");
  const closed = new Promise((resolve) =>
    fixtureProcess.once("close", resolve),
  );
  await Promise.race([closed, delay(5_000)]);
  if (fixtureProcess.exitCode === null) signalFixture("SIGKILL");
}

async function cleanup() {
  if (cleanupPromise) return cleanupPromise;
  stopping = true;
  cleanupPromise = (async () => {
    if (previewServer) {
      try {
        await previewServer.close();
      } catch {
        // Cleanup remains best effort after partial startup.
      }
    }
    await stopFixture();
    await command("docker", ["rm", "--force", containerName]);
  })();
  return cleanupPromise;
}

for (const [signal, exitCode] of [
  ["SIGINT", 130],
  ["SIGTERM", 143],
]) {
  process.once(signal, () => {
    void cleanup().then(() => process.exit(exitCode));
  });
}

process.once("exit", () => {
  if (fixtureProcess?.exitCode === null) {
    try {
      if (process.platform === "win32") fixtureProcess.kill("SIGKILL");
      else process.kill(-fixtureProcess.pid, "SIGKILL");
    } catch {
      // The fixture already stopped.
    }
  }
  spawnSync("docker", ["rm", "--force", containerName], { stdio: "ignore" });
});

try {
  const postgresPort = await startPostgres();
  const publicOrigin = await startPreview();
  const databaseURL = `postgresql://postgres:browser-test-only-password@127.0.0.1:${postgresPort}/memento?sslmode=disable`;
  apiURL = await startFixture(databaseURL, publicOrigin);
  stdout.write(`MEMENTO_BROWSER_URL=${publicOrigin}\n`);
} catch {
  process.stderr.write("Browser application fixture failed during startup.\n");
  await cleanup();
  process.exit(1);
}
