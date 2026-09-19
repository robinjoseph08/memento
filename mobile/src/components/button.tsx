import { Pressable, Text } from "react-native";

import { fonts, useTheme } from "@/theme";

// Button is the app's one pressable. The quiet variant is a text button for
// secondary actions.
export function Button({
  disabled = false,
  label,
  onPress,
  variant = "primary",
}: {
  disabled?: boolean;
  label: string;
  onPress: () => void;
  variant?: "primary" | "quiet";
}) {
  const theme = useTheme();
  const primary = variant === "primary";
  return (
    <Pressable
      accessibilityState={{ disabled }}
      disabled={disabled}
      onPress={onPress}
      role="button"
      style={({ pressed }) => ({
        alignItems: "center",
        backgroundColor: primary
          ? theme.primary
          : pressed
            ? theme.surface
            : "transparent",
        borderRadius: 10,
        justifyContent: "center",
        minHeight: 48,
        opacity: disabled ? 0.6 : pressed && primary ? 0.85 : 1,
        paddingHorizontal: 16,
      })}
    >
      <Text
        style={{
          color: primary ? theme.primaryForeground : theme.accentText,
          fontFamily: fonts.medium,
          fontSize: 16,
        }}
      >
        {label}
      </Text>
    </Pressable>
  );
}
