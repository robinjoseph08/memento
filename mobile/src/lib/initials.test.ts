import { avatarHue, initials } from "./initials";

it.each([
  ["Alex Family", "AF"],
  ["alex", "A"],
  ["  Alex  van  Dyke ", "AV"],
  ["", "?"],
])("shortens %j to %s", (name, letters) => {
  expect(initials(name)).toBe(letters);
});

it("gives the same name the same hue and different names different ones", () => {
  expect(avatarHue("Alex")).toBe(avatarHue(" alex "));
  expect(avatarHue("Alex") % 30).toBe(15);
  expect(avatarHue("Alex")).not.toBe(avatarHue("Robin"));
});
