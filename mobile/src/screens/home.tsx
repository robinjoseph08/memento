import { useConnection } from "@/providers";

import { AlbumsScreen } from "./albums";
import { ConnectScreen } from "./connect";
import { SignInScreen } from "./sign-in";

// Home is the app's first screen: the connect form until an Installation is
// connected, sign-in until the Person has a session there, then their Albums.
export function Home({ devAddress }: { devAddress: string }) {
  const { origin, token } = useConnection();
  if (!origin) {
    return <ConnectScreen devAddress={devAddress} />;
  }
  if (!token) {
    return <SignInScreen origin={origin} />;
  }
  return <AlbumsScreen origin={origin} token={token} />;
}
