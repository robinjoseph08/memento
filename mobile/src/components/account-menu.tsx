import { ActionSheetIOS, Alert, Platform, Pressable } from "react-native";

import type { MediaSource } from "@/lib/http";

import { Avatar } from "./avatar";

// AccountMenu is the avatar in the corner. Tapping it opens the platform's
// own sheet with Sign out, which lives here until the app has a settings
// screen. The name may be empty while the Person is still loading.
export function AccountMenu({
  avatar,
  name,
  onSignOut,
  signingOut,
}: {
  avatar?: MediaSource;
  name: string;
  onSignOut: () => void;
  signingOut: boolean;
}) {
  function open() {
    if (Platform.OS === "ios") {
      ActionSheetIOS.showActionSheetWithOptions(
        {
          cancelButtonIndex: 1,
          destructiveButtonIndex: 0,
          options: ["Sign out", "Cancel"],
          title: name || undefined,
        },
        (index) => {
          if (index === 0) {
            onSignOut();
          }
        },
      );
      return;
    }
    Alert.alert(name || "Account", undefined, [
      { style: "cancel", text: "Cancel" },
      { onPress: onSignOut, text: "Sign out" },
    ]);
  }
  return (
    <Pressable
      accessibilityLabel={signingOut ? "Signing out" : "Account menu"}
      accessibilityState={{ disabled: signingOut }}
      disabled={signingOut}
      hitSlop={8}
      onPress={open}
      role="button"
      style={({ pressed }) => ({ opacity: pressed || signingOut ? 0.6 : 1 })}
    >
      <Avatar name={name} source={avatar} />
    </Pressable>
  );
}
