import { codeFromReturn, signInURL } from "./sign-in";

it("opens the Installation's web sign-in with the return link", () => {
  expect(
    signInURL("https://photos.example.com", "memento://sign-in"),
  ).toBe(
    "https://photos.example.com/sign-in?return_to=memento%3A%2F%2Fsign-in",
  );
});

it.each([
  ["memento://sign-in?code=abc-123", "abc-123"],
  ["exp://192.168.1.20:8081/--/sign-in?code=abc%2B1", "abc+1"],
  ["memento://sign-in?from=web&code=xyz", "xyz"],
  ["memento://sign-in?code=xyz#fragment", "xyz"],
])("reads the code out of %s", (url, code) => {
  expect(codeFromReturn(url)).toBe(code);
});

it.each(["memento://sign-in", "memento://sign-in?code=", "memento://sign-in?decode=1"])(
  "finds no code in %s",
  (url) => {
    expect(codeFromReturn(url)).toBeNull();
  },
);
