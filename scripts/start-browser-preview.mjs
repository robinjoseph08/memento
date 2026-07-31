import { stdout } from "node:process";

import { preview } from "vite";

const server = await preview({
  preview: {
    host: "127.0.0.1",
    port: 0,
    strictPort: true,
  },
});
const address = server.httpServer.address();
if (!address || typeof address === "string") {
  await server.close();
  throw new Error("Browser preview did not allocate a TCP port.");
}
stdout.write(`MEMENTO_BROWSER_URL=http://127.0.0.1:${address.port}\n`);
