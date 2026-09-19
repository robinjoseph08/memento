import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createContext, use, useEffect, useState, type ReactNode } from "react";

import { createHTTP as createRealHTTP, type CreateHTTP } from "@/lib/http";
import {
  forgetInstallation,
  rememberInstallation,
  rememberedInstallation,
} from "@/lib/storage";

interface Connection {
  origin: string | null;
  connect(origin: string): Promise<void>;
  disconnect(): Promise<void>;
}

const HTTPContext = createContext<CreateHTTP>(createRealHTTP);
const ConnectionContext = createContext<Connection | null>(null);

// useCreateHTTP gives query definitions the HTTP adapter. Screens and
// components never build requests themselves.
export function useCreateHTTP() {
  return use(HTTPContext);
}

// useConnection is the origin of the Installation the app is connected to, if
// any. connect and disconnect also update what is remembered across restarts.
// What the server says about itself is server state and lives in queries.
export function useConnection() {
  const connection = use(ConnectionContext);
  if (!connection) {
    throw new Error("useConnection needs AppProviders above it");
  }
  return connection;
}

// AppProviders wraps the whole app. It renders nothing until it knows whether
// an Installation is remembered, so the first screen shown is the right one.
// Tests pass fakeHTTP's create; nothing else is replaced.
export function AppProviders({
  children,
  createHTTP = createRealHTTP,
}: {
  children: ReactNode;
  createHTTP?: CreateHTTP;
}) {
  const [queryClient] = useState(
    () =>
      new QueryClient({ defaultOptions: { queries: { staleTime: 60_000 } } }),
  );
  const [origin, setOrigin] = useState<string | null>();

  useEffect(() => {
    rememberedInstallation().then(setOrigin, () => setOrigin(null));
  }, []);

  if (origin === undefined) {
    return null;
  }
  const connection: Connection = {
    origin,
    async connect(found) {
      await rememberInstallation(found);
      setOrigin(found);
    },
    async disconnect() {
      if (origin) {
        await forgetInstallation(origin);
        queryClient.removeQueries({ queryKey: [origin] });
      }
      setOrigin(null);
    },
  };
  return (
    <QueryClientProvider client={queryClient}>
      <HTTPContext value={createHTTP}>
        <ConnectionContext value={connection}>{children}</ConnectionContext>
      </HTTPContext>
    </QueryClientProvider>
  );
}
