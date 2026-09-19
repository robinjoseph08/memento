import { useColorScheme } from "react-native";

// These values mirror the theme tokens in the web app's app/styles.css so the
// two feel related. Change them together.
const dark = {
  background: "#101112",
  foreground: "#edeeed",
  muted: "#a8aaa9",
  surface: "#191b1c",
  border: "#2d3032",
  accentText: "#4ac2d3",
  focus: "#06b6d4",
  destructive: "#ffaaa5",
  primary: "#06b6d4",
  primaryForeground: "#083344",
};

const light: typeof dark = {
  background: "#fafbfb",
  foreground: "#212629",
  muted: "#657077",
  surface: "#f0f2f2",
  border: "#dce2e3",
  accentText: "#08788d",
  focus: "#08788d",
  destructive: "#b32e2a",
  primary: "#06b6d4",
  primaryForeground: "#083344",
};

export const fonts = {
  body: "Montserrat_400Regular",
  medium: "Montserrat_500Medium",
  heading: "Slabo13px_400Regular",
};

// useTheme follows the phone's appearance setting. Memento is dark unless the
// phone asks for light, as on the web.
export function useTheme() {
  return useColorScheme() === "light" ? light : dark;
}
