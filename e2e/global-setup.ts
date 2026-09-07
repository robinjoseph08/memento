import { execFileSync } from "node:child_process";

export default function globalSetup() {
  // Workers only start compiled binaries; Vite is not part of the browser fixture.
  execFileSync("mise", ["build"], { stdio: "inherit" });
  execFileSync("mise", ["build:fixture"], { stdio: "inherit" });
}
