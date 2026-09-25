import { Image } from "expo-image";
import { Text, useColorScheme, View } from "react-native";

import { avatarHue, initials } from "@/lib/initials";
import type { MediaSource } from "@/lib/http";
import { fonts } from "@/theme";

// Avatar shows a Person's picture, or their initials on a tint that is
// theirs alone, as on the web. The hue comes from the name and the
// lightness from the theme.
export function Avatar({
  name,
  size = 32,
  source,
}: {
  name: string;
  size?: number;
  source?: MediaSource;
}) {
  const light = useColorScheme() === "light";
  const hue = avatarHue(name);
  const round = { borderRadius: size / 2, height: size, width: size };
  if (source) {
    return (
      <Image
        accessibilityLabel={name}
        cachePolicy="disk"
        contentFit="cover"
        source={source}
        style={round}
        transition={0}
      />
    );
  }
  return (
    <View
      accessibilityLabel={name}
      accessible
      role="img"
      style={{
        ...round,
        alignItems: "center",
        backgroundColor: light
          ? `hsl(${hue}, 45%, 88%)`
          : `hsl(${hue}, 30%, 34%)`,
        justifyContent: "center",
      }}
    >
      <Text
        style={{
          color: light ? `hsl(${hue}, 40%, 32%)` : `hsl(${hue}, 45%, 92%)`,
          fontFamily: fonts.medium,
          fontSize: size * 0.4,
        }}
      >
        {initials(name)}
      </Text>
    </View>
  );
}
