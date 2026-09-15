// Two-letter avatar fallback for a display name or Immich face name.
export function initials(name: string) {
  const letters = name
    .split(/\s+/)
    .filter(Boolean)
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
  return letters || "?";
}

// A hue in degrees that is the same for the same name, so a person's fallback
// avatar keeps its color everywhere they appear. Twelve hues, 30 degrees
// apart, keep any two people's colors clearly different.
export function avatarHue(name: string) {
  let hash = 0;
  for (const character of name.trim().toLowerCase())
    hash = Math.imul(hash ^ character.charCodeAt(0), 0x01000193) >>> 0;
  hash = Math.imul(hash ^ (hash >>> 15), 0x2c1b3c6d) >>> 0;
  return (hash % 12) * 30 + 15;
}
