import * as Linking from "expo-linking";
import * as WebBrowser from "expo-web-browser";
import { Platform } from "react-native";

// signInURL is the Installation's own web sign-in, told where to send the
// browser sheet back to when the Person is done.
export function signInURL(origin: string, returnTo: string) {
  return `${origin}/sign-in?return_to=${encodeURIComponent(returnTo)}`;
}

// codeFromReturn reads the single-use code out of the link the server sent
// the browser sheet back with. React Native's URL cannot read query strings,
// so this looks for the one parameter itself.
export function codeFromReturn(url: string) {
  const match = /[?&]code=([^&#]+)/.exec(url);
  return match ? decodeURIComponent(match[1]) : null;
}

// platformLabel names this phone in the Person's sessions on the web, as in
// "Memento on iPhone".
export function platformLabel() {
  if (Platform.OS === "ios") {
    return Platform.isPad ? "iPad" : "iPhone";
  }
  if (Platform.OS === "android") {
    return "Android";
  }
  return "the web";
}

// openSignIn shows the Installation's web sign-in in the system browser sheet
// and resolves with the code it came back with, or null when the Person
// closed the sheet instead. The return link is this app's own, so in Expo Go
// it is Expo Go's, which the server only accepts in development.
export async function openSignIn(origin: string) {
  const returnTo = Linking.createURL("sign-in");
  const result = await WebBrowser.openAuthSessionAsync(
    signInURL(origin, returnTo),
    returnTo,
  );
  if (result.type !== "success") {
    return null;
  }
  return codeFromReturn(result.url);
}
