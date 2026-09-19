import {
  Montserrat_400Regular,
  Montserrat_500Medium,
} from "@expo-google-fonts/montserrat";
import { Slabo13px_400Regular } from "@expo-google-fonts/slabo-13px";
import { useFonts } from "expo-font";
import { Stack } from "expo-router";
import * as SplashScreen from "expo-splash-screen";
import { StatusBar } from "expo-status-bar";

import { AppProviders } from "@/providers";
import { useTheme } from "@/theme";

void SplashScreen.preventAutoHideAsync();

export default function Layout() {
  const theme = useTheme();
  const [loaded, failed] = useFonts({
    Montserrat_400Regular,
    Montserrat_500Medium,
    Slabo13px_400Regular,
  });
  // A font that fails to load falls back to the system font rather than
  // leaving the splash screen up forever.
  if (!loaded && !failed) {
    return null;
  }
  return (
    <AppProviders>
      <StatusBar style="auto" />
      <Stack
        screenOptions={{
          contentStyle: { backgroundColor: theme.background },
          headerShown: false,
        }}
      />
    </AppProviders>
  );
}
