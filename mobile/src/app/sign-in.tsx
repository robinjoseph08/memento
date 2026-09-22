import { Redirect } from "expo-router";

// The server sends the browser sheet back to this link with the sign-in
// code. On iOS the sheet hands the link to the sign-in in progress; on
// Android the phone also opens the app with it, which lands here. The home
// screen finishes the sign-in either way, so this only returns there.
export default function SignInReturn() {
  return <Redirect href="/" />;
}
