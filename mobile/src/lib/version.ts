// The oldest Memento release this app can talk to. 0.2.0 was the last release
// whose status endpoint reported no version. Raise this only when the app
// starts to depend on something a newer server added; see ADR 0013.
export const MIN_SERVER_VERSION = "0.2.1";

export const UPDATE_NEEDED =
  "This Memento needs an update before the app can use it. Ask whoever runs your Memento to update it.";

// supportsServer is the app's one minimum-version check. Only releases are
// compared. Anything else, such as "dev" or what git describes between two
// tags or with uncommitted changes, comes from a maintainer's own machine and
// is let through.
export function supportsServer(version: string | undefined): version is string {
  if (!version) {
    return false;
  }
  const release = /^v?(\d+)\.(\d+)\.(\d+)(-.+)?$/.exec(version);
  if (!release || /-\d+-g[0-9a-f]+(-dirty)?$|-dirty$/.test(version)) {
    return true;
  }
  const minimum = MIN_SERVER_VERSION.split(".").map(Number);
  for (const [index, part] of release.slice(1, 4).map(Number).entries()) {
    if (part !== minimum[index]) {
      return part > minimum[index];
    }
  }
  return true;
}
