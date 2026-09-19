import { useConnection } from "@/providers";

import { ConnectScreen } from "./connect";
import { ConnectedScreen } from "./connected";

// Home is the app's first screen: the connect form until an Installation is
// connected, then that Installation.
export function Home({ devAddress }: { devAddress: string }) {
  const { origin } = useConnection();
  return origin ? (
    <ConnectedScreen origin={origin} />
  ) : (
    <ConnectScreen devAddress={devAddress} />
  );
}
