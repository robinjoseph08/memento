import { useCallback, useEffect } from "react";

import type { Tip } from "../types/generated/identity";
import { useIdentityStatus, useRetireTip } from "./queries/identity";

// useTip says whether a viewer should see a Tip now. ready means its target is
// on screen; used means they are using what it points at, which retires it
// without showing it. Curators never see Tips, including in their viewer
// preview. The three Tips never share a screen, so nothing queues them; a
// Tip that could appear beside another would need to.
export function useTip(
  tip: Tip,
  { ready = false, used = false }: { ready?: boolean; used?: boolean },
) {
  const person = useIdentityStatus().data?.person;
  const { mutate } = useRetireTip();
  const pending =
    !!person && !person.is_curator && !person.retired_tips.includes(tip);
  const retire = useCallback(() => {
    if (pending) mutate(tip);
  }, [pending, mutate, tip]);
  useEffect(() => {
    if (used) retire();
  }, [used, retire]);
  return { showing: pending && ready && !used, retire };
}
