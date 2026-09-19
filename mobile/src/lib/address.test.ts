import { parseAddress } from "./address";

describe("parseAddress", () => {
  it.each([
    ["https://photos.example.com", "https://photos.example.com"],
    ["  https://photos.example.com/  ", "https://photos.example.com"],
    ["photos.example.com", "https://photos.example.com"],
    ["Photos.Example.com", "https://photos.example.com"],
    ["https://photos.example.com:8443", "https://photos.example.com:8443"],
    // People paste whatever is in their browser's address bar.
    [
      "https://photos.example.com/albums/123?tab=videos#top",
      "https://photos.example.com",
    ],
  ])("reads %j as the Installation origin %j", (input, origin) => {
    expect(parseAddress(input, { allowHTTP: false })).toEqual({ origin });
  });

  it("accepts plain http in development", () => {
    expect(
      parseAddress("http://192.168.1.20:5173", { allowHTTP: true }),
    ).toEqual({
      origin: "http://192.168.1.20:5173",
    });
  });

  it("requires https outside development", () => {
    expect(
      parseAddress("http://photos.example.com", { allowHTTP: false }),
    ).toEqual({
      error:
        "Memento addresses start with https://. Check the address and try again.",
    });
  });

  it("asks for an address when the field is empty", () => {
    expect(parseAddress("   ", { allowHTTP: false })).toEqual({
      error: "Enter your Memento address.",
    });
  });

  it.each([
    "ftp://photos.example.com",
    "memento://photos.example.com",
    "not an address",
    "https://",
  ])("rejects %j", (input) => {
    expect(parseAddress(input, { allowHTTP: true })).toEqual({
      error: "That doesn't look like a web address. Check it and try again.",
    });
  });
});
