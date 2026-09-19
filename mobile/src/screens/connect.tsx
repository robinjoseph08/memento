import { useState } from "react";
import { Text, TextInput, View } from "react-native";

import { Button } from "@/components/button";
import { Screen } from "@/components/screen";
import { Body, ErrorText, Heading } from "@/components/text";
import { useConnect } from "@/hooks/queries/installation";
import { parseAddress } from "@/lib/address";
import { fonts, useTheme } from "@/theme";

// ConnectScreen asks for an Installation's address and checks it before
// anything else happens. devAddress prefills the field in development.
export function ConnectScreen({ devAddress }: { devAddress: string }) {
  const theme = useTheme();
  const [address, setAddress] = useState(devAddress);
  const [invalid, setInvalid] = useState("");
  const [focused, setFocused] = useState(false);
  const connect = useConnect();
  const error = invalid || connect.error?.message;

  function submit() {
    if (connect.isPending) {
      return;
    }
    // Production builds never accept plain http.
    const parsed = parseAddress(address, { allowHTTP: __DEV__ });
    connect.reset();
    if ("error" in parsed) {
      setInvalid(parsed.error);
      return;
    }
    setInvalid("");
    connect.mutate(parsed.origin);
  }

  return (
    <Screen>
      <View style={{ gap: 8 }}>
        <Heading>Connect to your Memento</Heading>
        <Body>Enter the web address you use to open Memento.</Body>
      </View>
      <View style={{ gap: 12 }}>
        <Text
          nativeID="address-label"
          style={{
            color: theme.foreground,
            fontFamily: fonts.medium,
            fontSize: 14,
          }}
        >
          Memento address
        </Text>
        <TextInput
          accessibilityLabel="Memento address"
          accessibilityLabelledBy="address-label"
          autoCapitalize="none"
          autoComplete="off"
          autoCorrect={false}
          inputMode="url"
          onBlur={() => setFocused(false)}
          onChangeText={setAddress}
          onFocus={() => setFocused(true)}
          onSubmitEditing={submit}
          placeholder="https://photos.example.com"
          placeholderTextColor={theme.muted}
          returnKeyType="go"
          style={{
            backgroundColor: theme.surface,
            borderColor: error
              ? theme.destructive
              : focused
                ? theme.focus
                : theme.border,
            borderRadius: 10,
            borderWidth: 1,
            color: theme.foreground,
            fontFamily: fonts.body,
            fontSize: 16,
            minHeight: 48,
            paddingHorizontal: 14,
          }}
          textContentType="URL"
          value={address}
        />
        {error ? <ErrorText>{error}</ErrorText> : null}
        <Button
          disabled={connect.isPending}
          label={connect.isPending ? "Connecting…" : "Connect"}
          onPress={submit}
        />
      </View>
    </Screen>
  );
}
