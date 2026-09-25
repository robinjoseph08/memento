import { useQueryClient } from "@tanstack/react-query";
import { Image } from "expo-image";
import { CalendarDays, Image as Photo, SquarePlay } from "lucide-react-native";
import { FlatList, Text, useWindowDimensions, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { AccountMenu } from "@/components/account-menu";
import { Button } from "@/components/button";
import { Body, ErrorText, Heading } from "@/components/text";
import { Wordmark } from "@/components/wordmark";
import { useAlbums, useMe, useSignOut } from "@/hooks/queries/session";
import type { MediaSource } from "@/lib/http";
import { captureRange, mediaCounts } from "@/lib/labels";
import { useHTTP } from "@/providers";
import { fonts, useTheme } from "@/theme";
import type { ViewerAlbum } from "@/types/generated/publishing";

const GUTTER = 24;
const GAP = 16;

// AlbumsScreen is the gallery of every Album the Person can see, newest
// first, laid out as on the web: a square cover with the title, the capture
// days, and the counts under it.
export function AlbumsScreen({
  origin,
  token,
}: {
  origin: string;
  token: string;
}) {
  const theme = useTheme();
  const insets = useSafeAreaInsets();
  const { width } = useWindowDimensions();
  const http = useHTTP(origin, token);
  const queryClient = useQueryClient();
  const albums = useAlbums(origin, token);
  const me = useMe(origin, token);
  const signOut = useSignOut(origin, token);
  // Pulling down refreshes everything shown here, the Person included.
  const refresh = () => queryClient.invalidateQueries({ queryKey: [origin] });
  const columns = width >= 600 ? 3 : 2;
  const tile = (width - GUTTER * 2 - GAP * (columns - 1)) / columns;

  return (
    <View
      style={{
        backgroundColor: theme.background,
        flex: 1,
        paddingTop: insets.top + 12,
      }}
    >
      <View
        style={{
          alignItems: "center",
          flexDirection: "row",
          justifyContent: "space-between",
          paddingBottom: 8,
          paddingHorizontal: GUTTER,
        }}
      >
        <Wordmark />
        <AccountMenu
          avatar={
            me.data?.avatar_url ? http.media(me.data.avatar_url) : undefined
          }
          name={me.data?.display_name ?? ""}
          onSignOut={() => signOut.mutate()}
          signingOut={signOut.isPending}
        />
      </View>
      <FlatList
        columnWrapperStyle={{ gap: GAP }}
        contentContainerStyle={{
          flexGrow: 1,
          gap: 28,
          paddingBottom: insets.bottom + 24,
          paddingHorizontal: GUTTER,
          paddingTop: 16,
        }}
        data={albums.data ?? []}
        key={columns}
        keyExtractor={(album) => album.id}
        ListEmptyComponent={
          albums.isPending ? (
            <Text
              role="status"
              style={{
                color: theme.muted,
                fontFamily: fonts.body,
                fontSize: 16,
              }}
            >
              Loading albums…
            </Text>
          ) : albums.isError ? (
            <View style={{ gap: 12 }}>
              <ErrorText>We can&apos;t reach your Memento right now.</ErrorText>
              <Button
                disabled={albums.isFetching}
                label={albums.isFetching ? "Trying…" : "Try again"}
                onPress={() => void albums.refetch()}
              />
            </View>
          ) : (
            <Body>
              Nothing is shared with you yet. Your Curator will choose what to
              share, and you&apos;ll hear about it here.
            </Body>
          )
        }
        ListHeaderComponent={
          <View style={{ gap: 6, paddingBottom: 8 }}>
            <Heading>Your albums</Heading>
            <Body>Here are all the albums that have been shared with you.</Body>
          </View>
        }
        numColumns={columns}
        onRefresh={() => void refresh()}
        refreshing={albums.isRefetching}
        renderItem={({ item }) => (
          <AlbumCard
            album={item}
            cover={item.cover_url ? http.media(item.cover_url) : undefined}
            size={tile}
          />
        )}
      />
    </View>
  );
}

function AlbumCard({
  album,
  cover,
  size,
}: {
  album: ViewerAlbum;
  cover?: MediaSource;
  size: number;
}) {
  const theme = useTheme();
  const square = {
    backgroundColor: theme.surface,
    borderRadius: 6,
    height: size,
    width: size,
  };
  const detail = {
    color: theme.muted,
    fontFamily: fonts.body,
    fontSize: 12,
    lineHeight: 18,
  };
  return (
    <View style={{ gap: 4, width: size }}>
      {cover ? (
        <Image
          accessibilityLabel={album.title}
          cachePolicy="disk"
          contentFit="cover"
          source={cover}
          style={square}
          transition={0}
        />
      ) : (
        <View
          style={{ ...square, alignItems: "center", justifyContent: "center" }}
        >
          <Text style={detail}>No cover</Text>
        </View>
      )}
      <Text
        style={{
          color: theme.foreground,
          fontFamily: fonts.heading,
          fontSize: 18,
          marginTop: 8,
        }}
      >
        {album.title}
      </Text>
      <View style={{ alignItems: "flex-start", flexDirection: "row", gap: 4 }}>
        <View style={{ paddingTop: 2 }}>
          <CalendarDays color={theme.muted} size={14} strokeWidth={1.5} />
        </View>
        <Text style={{ ...detail, flex: 1 }}>{captureRange(album)}</Text>
      </View>
      <View
        accessibilityLabel={mediaCounts(album)}
        accessible
        style={{ alignItems: "center", flexDirection: "row", gap: 12 }}
      >
        <View style={{ alignItems: "center", flexDirection: "row", gap: 4 }}>
          <Photo color={theme.muted} size={14} strokeWidth={1.5} />
          <Text style={detail}>{album.photo_count}</Text>
        </View>
        <View style={{ alignItems: "center", flexDirection: "row", gap: 4 }}>
          <SquarePlay color={theme.muted} size={14} strokeWidth={1.5} />
          <Text style={detail}>{album.video_count}</Text>
        </View>
      </View>
    </View>
  );
}
