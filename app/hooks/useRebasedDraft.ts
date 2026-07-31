import { useCallback, useMemo, useState } from "react";

type DraftState<T> = {
  base: T;
  edited: T;
  dirty: Set<keyof T>;
};

function rebase<T extends object>(state: DraftState<T>, server: T): T {
  return Object.fromEntries(
    Object.keys(server).map((key) => {
      const field = key as keyof T;
      return [
        key,
        state.dirty.has(field) ? state.edited[field] : server[field],
      ];
    }),
  ) as T;
}

export function useRebasedDraft<T extends object>(server: T | undefined) {
  const [state, setState] = useState<DraftState<T>>();
  const draft = useMemo(
    () => (state && server ? rebase(state, server) : (state?.edited ?? server)),
    [server, state],
  );
  const hasStaleConflict = useMemo(
    () =>
      Boolean(
        state &&
        server &&
        Object.keys(server).some((key) => {
          const field = key as keyof T;
          return (
            state.dirty.has(field) &&
            !Object.is(server[field], state.base[field]) &&
            !Object.is(server[field], state.edited[field])
          );
        }),
      ),
    [server, state],
  );
  const setDraft = useCallback(
    (next: T) => {
      setState((current) => {
        const currentDraft =
          current && server
            ? rebase(current, server)
            : (current?.edited ?? server);
        const base = { ...(current?.base ?? server ?? next) };
        const dirty = new Set(current?.dirty);

        for (const key of Object.keys(next)) {
          const field = key as keyof T;
          if (currentDraft && Object.is(next[field], currentDraft[field]))
            continue;
          if (server && Object.is(next[field], server[field])) {
            dirty.delete(field);
            base[field] = server[field];
          } else {
            if (!dirty.has(field) && server) base[field] = server[field];
            dirty.add(field);
          }
        }
        return { base, edited: next, dirty };
      });
    },
    [server],
  );
  const acceptServerDraft = useCallback((next: T) => {
    setState({ base: next, edited: next, dirty: new Set() });
  }, []);
  const resetToServer = useCallback(() => {
    if (server) setState({ base: server, edited: server, dirty: new Set() });
  }, [server]);
  return {
    draft,
    setDraft,
    acceptServerDraft,
    hasStaleConflict,
    resetToServer,
  };
}
