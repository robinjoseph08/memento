import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { test as base, expect, type APIRequestContext } from "@playwright/test";

type Installation = { apiURL: string; fixtureURL: string };
type CheckpointName = "asset-metadata" | "import-release";
type CheckpointState = { mode: string; hits: number; waiting: number };

function immichControls(request: APIRequestContext, url: string) {
  return {
    async online() {
      expect(
        (
          await request.post(`${url}/__fixture/state`, {
            data: { available: true },
          })
        ).status(),
      ).toBe(200);
    },
    async checkpoint(name: CheckpointName, mode: "open" | "pause" | "fail") {
      expect(
        (
          await request.post(`${url}/__fixture/checkpoints/${name}`, {
            data: { mode },
          })
        ).status(),
      ).toBe(200);
    },
    async checkpoints() {
      const response = await request.get(`${url}/__fixture/checkpoints`);
      expect(response.status()).toBe(200);
      return (await response.json()) as Record<CheckpointName, CheckpointState>;
    },
    async requests() {
      const response = await request.get(`${url}/__fixture/requests`);
      expect(response.status()).toBe(200);
      return (await response.json()) as Record<string, number>;
    },
    async restart() {
      expect((await request.post(`${url}/__fixture/restart`)).status()).toBe(
        204,
      );
    },
  };
}

function isInstallation(value: unknown): value is Installation {
  return (
    typeof value === "object" &&
    value !== null &&
    "apiURL" in value &&
    typeof value.apiURL === "string" &&
    "fixtureURL" in value &&
    typeof value.fixtureURL === "string"
  );
}

// Workers may run several tests. Allocate a fresh installation for each test
// so an earlier claim never changes the next test's starting state.
export const test = base.extend<{
  fixtureURL: string;
  installation: Installation;
  immich: ReturnType<typeof immichControls>;
}>({
  installation: [
    async ({ browserName }, use) => {
      const child = spawn("./build/fixture/fixture", ["--offline"], {
        stdio: ["ignore", "pipe", "pipe"],
      });
      let logs = "";
      child.stderr.on("data", (chunk: Buffer) => {
        logs = (logs + chunk.toString()).slice(-20_000);
      });
      const exited = new Promise<void>((resolve) => {
        child.once("exit", () => resolve());
        child.once("error", () => resolve());
      });
      const lines = createInterface({ input: child.stdout });
      try {
        const installation = await new Promise<Installation>(
          (resolve, reject) => {
            const timeout = setTimeout(
              () =>
                reject(
                  new Error(
                    `${browserName} fixture readiness timed out\n${logs}`,
                  ),
                ),
              45_000,
            );
            const finish = () => clearTimeout(timeout);
            child.once("error", (error) => {
              finish();
              reject(error);
            });
            child.once("exit", (code) => {
              finish();
              reject(new Error(`Fixture exited with ${code}\n${logs}`));
            });
            lines.on("line", (line) => {
              try {
                const value: unknown = JSON.parse(line);
                if (isInstallation(value)) {
                  finish();
                  resolve(value);
                }
              } catch {
                // Only the JSON readiness record is part of the startup protocol.
              }
            });
          },
        );
        await use(installation);
      } finally {
        lines.close();
        child.kill("SIGTERM");
        const timeout = setTimeout(() => child.kill("SIGKILL"), 40_000);
        await exited;
        clearTimeout(timeout);
      }
    },
    { timeout: 65_000 },
  ],
  baseURL: async ({ installation }, use) => {
    await use(installation.apiURL);
  },
  fixtureURL: async ({ installation }, use) => {
    await use(installation.fixtureURL);
  },
  immich: async ({ request, fixtureURL }, use) => {
    await use(immichControls(request, fixtureURL));
  },
});

export { expect };
