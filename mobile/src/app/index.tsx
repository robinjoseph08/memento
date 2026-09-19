import * as SplashScreen from "expo-splash-screen";
import { useEffect } from "react";

import { Home } from "@/screens/home";

export default function Index() {
  // This route first renders once fonts and the remembered Installation are
  // ready, so the splash screen hands over to a finished screen.
  useEffect(() => SplashScreen.hide(), []);
  // `mise start:mobile` sets this to the dev server's address on the network.
  return (
    <Home devAddress={process.env.EXPO_PUBLIC_DEV_INSTALLATION_URL ?? ""} />
  );
}
