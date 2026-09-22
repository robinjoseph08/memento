import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createContext, use, useEffect, useState, type ReactNode } from "react";

import {
  createHTTP as createRealHTTP,
  HTTPError,
  type CreateHTTP,
} from "@/lib/http";
import { clearCachedImages } from "@/lib/images";
import {
  forgetInstallation,
  forgetToken,
  rememberedInstallation,
  rememberedToken,
  rememberInstallation,
  rememberToken,
} from "@/lib/storage";

interface Connection {
  origin: string | null;
  // token is the Person's session at origin, or null while signed out.
  token: string | null;
  connect(origin: string): Promise<void>;
  disconnect(): Promise<void>;
  startSession(token: string): Promise<void>;
  endSession(): Promise<void>;
}

const HTTPContext = createContext<CreateHTTP>(createRealHTTP);
const ConnectionContext = createContext<Connection | null>(null);

// useCreateHTTP gives query definitions the HTTP adapter. Screens and
// components never build requests themselves.
export function useCreateHTTP() {
  return use(HTTPContext);
}

// useHTTP is the adapter bound to a connected Installation and session, for
// query definitions and for resolving the media in what they return.
export function useHTTP(origin: string, token: string | null = null) {
  return useCreateHTTP()(origin, token);
}

// useConnection is the Installation the app is connected to and the session
// it holds there, if any. The four actions also update what is remembered
// across restarts. What the server says about itself is server state and
// lives in queries.
export function useConnection() {
  const connection = use(ConnectionContext);
  if (!connection) {
    throw new Error("useConnection needs AppProviders above it");
  }
  return connection;
}

interface Remembered {
  origin: string | null;
  token: string | null;
}

async function remembered(): Promise<Remembered> {
  const origin = await rememberedInstallation();
  return { origin, token: origin ? await rememberedToken(origin) : null };
}

// forgetSession drops everything the session left on the phone: the token,
// the server state read with it, and the photos it fetched.
async function forgetSession(origin: string, queryClient: QueryClient) {
  await forgetToken(origin);
  queryClient.removeQueries({ queryKey: [origin] });
  await clearCachedImages();
}

// AppProviders wraps the whole app. It renders nothing until it knows whether
// an Installation and a session are remembered, so the first screen shown is
// the right one. Tests pass fakeHTTP's create; nothing else is replaced.
export function AppProviders({
  children,
  createHTTP = createRealHTTP,
}: {
  children: ReactNode;
  createHTTP?: CreateHTTP;
}) {
  const [state, setState] = useState<Remembered>();
  const [queryClient] = useState(
    () =>
      new QueryClient({ defaultOptions: { queries: { staleTime: 60_000 } } }),
  );

  useEffect(() => {
    remembered().then(setState, () => setState({ origin: null, token: null }));
  }, []);

  const connection: Connection | null = state
    ? {
        origin: state.origin,
        token: state.token,
        async connect(found) {
          await rememberInstallation(found);
          setState({ origin: found, token: await rememberedToken(found) });
        },
        async disconnect() {
          if (state.origin) {
            await forgetInstallation(state.origin);
            queryClient.removeQueries({ queryKey: [state.origin] });
          }
          setState({ origin: null, token: null });
        },
        async startSession(token) {
          if (!state.origin) {
            return;
          }
          await rememberToken(state.origin, token);
          setState({ origin: state.origin, token });
        },
        async endSession() {
          if (!state.origin) {
            return;
          }
          await forgetSession(state.origin, queryClient);
          setState({ origin: state.origin, token: null });
        },
      }
    : null;

  // When the server refuses the session, whatever asked, the app returns to
  // sign-in without losing the Installation.
  useEffect(() => {
    if (!state?.origin || !state.token) {
      return;
    }
    const { origin } = state;
    return queryClient.getQueryCache().subscribe((event) => {
      if (
        event.type === "updated" &&
        event.action.type === "error" &&
        event.action.error instanceof HTTPError &&
        event.action.error.status === 401 &&
        event.query.queryKey[0] === origin
      ) {
        void forgetSession(origin, queryClient).then(() =>
          setState({ origin, token: null }),
        );
      }
    });
  }, [state, queryClient]);

  if (!connection) {
    return null;
  }
  return (
    <QueryClientProvider client={queryClient}>
      <HTTPContext value={createHTTP}>
        <ConnectionContext value={connection}>{children}</ConnectionContext>
      </HTTPContext>
    </QueryClientProvider>
  );
}
