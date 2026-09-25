import { useState } from "react";
import { Modal, Pressable, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import type { MediaSource } from "@/lib/http";
import { fonts, useTheme } from "@/theme";

import { Avatar } from "./avatar";

// AccountMenu is the avatar in the corner and the small menu behind it, as
// on the web. Sign out lives here until the app has a settings screen.
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
  const theme = useTheme();
  const insets = useSafeAreaInsets();
  const [open, setOpen] = useState(false);
  return (
    <>
      <Pressable
        accessibilityLabel="Account menu"
        accessibilityState={{ expanded: open }}
        hitSlop={8}
        onPress={() => setOpen(true)}
        role="button"
        style={({ pressed }) => ({ opacity: pressed ? 0.7 : 1 })}
      >
        <Avatar name={name} source={avatar} />
      </Pressable>
      <Modal
        animationType="fade"
        onRequestClose={() => setOpen(false)}
        transparent
        visible={open}
      >
        <Pressable
          accessibilityLabel="Close menu"
          onPress={() => setOpen(false)}
          style={{ flex: 1 }}
        >
          <View
            role="menu"
            style={{
              backgroundColor: theme.surface,
              borderColor: theme.border,
              borderRadius: 12,
              borderWidth: 1,
              marginRight: 24,
              marginTop: insets.top + 52,
              minWidth: 200,
              paddingVertical: 6,
              position: "absolute",
              right: 0,
              top: 0,
            }}
          >
            <Text
              style={{
                color: theme.muted,
                fontFamily: fonts.medium,
                fontSize: 13,
                paddingHorizontal: 16,
                paddingVertical: 8,
              }}
            >
              {name}
            </Text>
            <Pressable
              accessibilityState={{ disabled: signingOut }}
              disabled={signingOut}
              onPress={() => {
                setOpen(false);
                onSignOut();
              }}
              role="menuitem"
              style={({ pressed }) => ({
                backgroundColor: pressed ? theme.border : "transparent",
                paddingHorizontal: 16,
                paddingVertical: 12,
              })}
            >
              <Text
                style={{
                  color: theme.foreground,
                  fontFamily: fonts.body,
                  fontSize: 16,
                }}
              >
                {signingOut ? "Signing out…" : "Sign out"}
              </Text>
            </Pressable>
          </View>
        </Pressable>
      </Modal>
    </>
  );
}
