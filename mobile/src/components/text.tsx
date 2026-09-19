import type { ReactNode } from "react";
import { Text } from "react-native";

import { fonts, useTheme } from "@/theme";

export function Heading({ children }: { children: ReactNode }) {
  const theme = useTheme();
  return (
    <Text
      role="heading"
      style={{
        color: theme.foreground,
        fontFamily: fonts.heading,
        fontSize: 30,
      }}
    >
      {children}
    </Text>
  );
}

export function Body({ children }: { children: ReactNode }) {
  const theme = useTheme();
  return (
    <Text
      style={{
        color: theme.muted,
        fontFamily: fonts.body,
        fontSize: 16,
        lineHeight: 24,
      }}
    >
      {children}
    </Text>
  );
}

// ErrorText announces itself to screen readers when it appears.
export function ErrorText({ children }: { children: ReactNode }) {
  const theme = useTheme();
  return (
    <Text
      accessibilityLiveRegion="polite"
      role="alert"
      style={{
        color: theme.destructive,
        fontFamily: fonts.body,
        fontSize: 14,
        lineHeight: 20,
      }}
    >
      {children}
    </Text>
  );
}
