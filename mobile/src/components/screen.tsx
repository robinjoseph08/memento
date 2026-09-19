import type { ReactNode } from "react";
import { KeyboardAvoidingView, Platform, ScrollView } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useTheme } from "@/theme";

import { Wordmark } from "./wordmark";

// Screen is the frame for the screens shown before sign-in: the wordmark on
// top and the content under it, clear of the notch and the keyboard.
export function Screen({ children }: { children: ReactNode }) {
  const theme = useTheme();
  const insets = useSafeAreaInsets();
  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ backgroundColor: theme.background, flex: 1 }}
    >
      <ScrollView
        contentContainerStyle={{
          flexGrow: 1,
          gap: 32,
          paddingBottom: insets.bottom + 24,
          paddingHorizontal: 24,
          paddingTop: insets.top + 16,
        }}
        keyboardShouldPersistTaps="handled"
      >
        <Wordmark />
        {children}
      </ScrollView>
    </KeyboardAvoidingView>
  );
}
