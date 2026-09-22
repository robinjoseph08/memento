import { Image } from "expo-image";
import { FlatList, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { Button } from "@/components/button";
import { Body, ErrorText, Heading } from "@/components/text";
import { useAlbums, useSignOut } from "@/hooks/queries/session";
import { mediaCounts } from "@/lib/labels";
import { useHTTP } from "@/providers";
import { fonts, useTheme } from "@/theme";
import type { ViewerAlbum } from "@/types/generated/publishing";

// AlbumsScreen lists every Album the Person can see, newest first, with its
// cover. Sign out lives here until the app has a settings screen.
export function AlbumsScreen({
  origin,
  token,
}: {
  origin: string;
  token: string;
}) {
  const theme = useTheme();
  const insets = useSafeAreaInsets();
  const albums = useAlbums(origin, token);
  const signOut = useSignOut(origin, token);

  return (
    <View
      style={{
        backgroundColor: theme.background,
        flex: 1,
        paddingTop: insets.top + 8,
      }}
    >
      <View
        style={{
          alignItems: "center",
          flexDirection: "row",
          justifyContent: "space-between",
          paddingHorizontal: 24,
          paddingVertical: 8,
        }}
      >
        <Heading>Albums</Heading>
        <Button
          disabled={signOut.isPending}
          label={signOut.isPending ? "Signing out…" : "Sign out"}
          onPress={() => signOut.mutate()}
          variant="quiet"
        />
      </View>
      {albums.isPending ? (
        <View style={{ paddingHorizontal: 24, paddingVertical: 16 }}>
          <Text
            role="status"
            style={{ color: theme.muted, fontFamily: fonts.body, fontSize: 16 }}
          >
            Loading albums…
          </Text>
        </View>
      ) : albums.isError ? (
        <View style={{ gap: 12, paddingHorizontal: 24, paddingVertical: 16 }}>
          <ErrorText>We can&apos;t reach your Memento right now.</ErrorText>
          <Button
            disabled={albums.isFetching}
            label={albums.isFetching ? "Trying…" : "Try again"}
            onPress={() => void albums.refetch()}
          />
        </View>
      ) : albums.data.length === 0 ? (
        <View style={{ paddingHorizontal: 24, paddingVertical: 16 }}>
          <Body>
            Nothing is shared with you yet. Your Curator will choose what to
            share, and you&apos;ll hear about it here.
          </Body>
        </View>
      ) : (
        <FlatList
          contentContainerStyle={{ paddingBottom: insets.bottom + 24 }}
          data={albums.data}
          keyExtractor={(album) => album.id}
          onRefresh={() => void albums.refetch()}
          refreshing={albums.isRefetching}
          renderItem={({ item }) => (
            <AlbumRow album={item} origin={origin} token={token} />
          )}
        />
      )}
    </View>
  );
}

function AlbumRow({
  album,
  origin,
  token,
}: {
  album: ViewerAlbum;
  origin: string;
  token: string;
}) {
  const theme = useTheme();
  const http = useHTTP(origin, token);
  const cover = { backgroundColor: theme.surface, borderRadius: 8, height: 64, width: 64 };
  return (
    <View
      style={{
        alignItems: "center",
        flexDirection: "row",
        gap: 16,
        paddingHorizontal: 24,
        paddingVertical: 12,
      }}
    >
      {album.cover_url ? (
        <Image
          accessibilityLabel={album.title}
          cachePolicy="disk"
          contentFit="cover"
          source={http.media(album.cover_url)}
          style={cover}
          transition={0}
        />
      ) : (
        <View style={cover} />
      )}
      <View style={{ flex: 1, gap: 4 }}>
        <Text
          style={{
            color: theme.foreground,
            fontFamily: fonts.heading,
            fontSize: 20,
          }}
        >
          {album.title}
        </Text>
        <Text
          style={{ color: theme.muted, fontFamily: fonts.body, fontSize: 13 }}
        >
          {mediaCounts(album)}
        </Text>
      </View>
    </View>
  );
}
