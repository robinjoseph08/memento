import { Image } from "expo-image";

// clearCachedImages drops every photo the image component has kept on this
// phone. Signing out calls it so nothing of the Person's stays behind.
export async function clearCachedImages() {
  await Promise.all([Image.clearMemoryCache(), Image.clearDiskCache()]);
}
