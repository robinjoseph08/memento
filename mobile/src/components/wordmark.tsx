import { Text, View } from "react-native";
import Svg, { Path, Rect } from "react-native-svg";

import { fonts, useTheme } from "@/theme";

// Wordmark is the brand mark from the web header: the Frames outline beside
// the lowercase name. This graphic is the only place the name is lowercase.
export function Wordmark() {
  const theme = useTheme();
  return (
    <View
      accessibilityLabel="Memento"
      accessible
      role="img"
      style={{ alignItems: "center", flexDirection: "row", gap: 11 }}
    >
      <Svg
        fill="none"
        height={36}
        stroke={theme.primary}
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={3}
        viewBox="0 0 32 32"
        width={36}
      >
        <Path d="M5 22V8a4 4 0 0 1 4-4h13" />
        <Rect height={19} rx={3} width={19} x={11} y={10} />
      </Svg>
      <Text
        style={{
          color: theme.foreground,
          fontFamily: fonts.heading,
          fontSize: 29,
          letterSpacing: -0.7,
        }}
      >
        memento
      </Text>
    </View>
  );
}
