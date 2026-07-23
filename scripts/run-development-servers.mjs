import { spawn } from "node:child_process";
import console from "node:console";
import process from "node:process";
import { clearTimeout, setTimeout } from "node:timers";

const command = process.argv[2] ?? "mise";
const commandArguments = process.argv[2]
  ? process.argv.slice(3)
  : ["run", "start:servers"];

const child = spawn(command, commandArguments, {
  detached: true,
  stdio: "inherit",
});

let stoppingSignal;
let forceTimer;

function signalGroup(signal) {
  try {
    process.kill(-child.pid, signal);
  } catch (error) {
    if (error.code !== "ESRCH") {
      throw error;
    }
  }
}

function stop(signal) {
  if (stoppingSignal) {
    return;
  }
  stoppingSignal = signal;
  signalGroup(signal);
  forceTimer = setTimeout(() => signalGroup("SIGKILL"), 10_000);
}

process.on("SIGINT", () => stop("SIGINT"));
process.on("SIGTERM", () => stop("SIGTERM"));

child.on("error", (error) => {
  console.error(`Unable to start development servers: ${error.message}`);
  process.exitCode = 1;
});

child.on("exit", (code) => {
  clearTimeout(forceTimer);
  if (stoppingSignal === "SIGINT") {
    process.exitCode = 130;
  } else if (stoppingSignal === "SIGTERM") {
    process.exitCode = 143;
  } else {
    process.exitCode = code ?? 1;
  }
});
