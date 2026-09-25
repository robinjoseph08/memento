import { Text, View } from "react-native";

import { Button } from "@/components/button";
import { Screen } from "@/components/screen";
import { Body, ErrorText, Heading } from "@/components/text";
import { useInstallationStatus } from "@/hooks/queries/installation";
import { useSignIn } from "@/hooks/queries/session";
import { UPDATE_NEEDED } from "@/lib/version";
import { useConnection } from "@/providers";
import { fonts, useTheme } from "@/theme";

// SignInScreen shows the Installation the app is connected to, checks that it
// still answers, and starts the web sign-in in the browser sheet.
export function SignInScreen({ origin }: { origin: string }) {
  const theme = useTheme();
  const { disconnect } = useConnection();
  const status = useInstallationStatus(origin);
  const signIn = useSignIn(origin);

  return (
    <Screen>
      <View style={{ gap: 8 }}>
        <Heading>Connected to Memento</Heading>
        <Text
          selectable
          style={{
            color: theme.accentText,
            fontFamily: fonts.medium,
            fontSize: 18,
          }}
        >
          {origin.replace(/^https:\/\//, "")}
        </Text>
        {status.data ? (
          <Body>Version {status.data.version.replace(/^v(?=\d)/, "")}</Body>
        ) : null}
      </View>
      {status.isError ? (
        <View style={{ gap: 12 }}>
          <ErrorText>
            {status.error.message === UPDATE_NEEDED
              ? UPDATE_NEEDED
              : "We can't reach your Memento right now."}
          </ErrorText>
          <Button
            disabled={status.isFetching}
            label={status.isFetching ? "Trying…" : "Try again"}
            onPress={() => void status.refetch()}
          />
        </View>
      ) : (
        <View style={{ gap: 12 }}>
          <Body>Sign in the same way you do on the web.</Body>
          {signIn.isError ? (
            <ErrorText>{signIn.error.message}</ErrorText>
          ) : null}
          <Button
            disabled={signIn.isPending || !status.data}
            label={signIn.isPending ? "Signing in…" : "Sign in"}
            onPress={() => signIn.mutate()}
          />
        </View>
      )}
      <Button
        label="Use a different address"
        onPress={() => void disconnect()}
        variant="quiet"
      />
    </Screen>
  );
}
