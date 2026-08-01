export const sourceKeys = {
  all: (identityGeneration: string) => ["sources", identityGeneration] as const,
  list: (identityGeneration: string, disposition: string) =>
    ["sources", identityGeneration, disposition] as const,
  mediaRoot: (identityGeneration: string) =>
    ["source-media", identityGeneration] as const,
  mediaSelection: (identityGeneration: string, sourceIDs: string[]) =>
    ["source-media", identityGeneration, ...sourceIDs] as const,
};

export const eventKeys = {
  all: (identityGeneration: string) => ["events", identityGeneration] as const,
  details: (identityGeneration: string) =>
    ["event", identityGeneration] as const,
  detail: (identityGeneration: string, eventID: string) =>
    ["event", identityGeneration, eventID] as const,
  previewRecipientsRoot: (identityGeneration: string) =>
    ["preview-recipients", identityGeneration] as const,
  previewRecipients: (identityGeneration: string, eventID: string) =>
    ["preview-recipients", identityGeneration, eventID] as const,
  recipientPreviews: (identityGeneration: string) =>
    ["event-preview", identityGeneration] as const,
  recipientPreview: (
    identityGeneration: string,
    eventID: string,
    version: number | undefined,
    recipientID: string,
  ) =>
    [
      "event-preview",
      identityGeneration,
      eventID,
      version,
      recipientID,
    ] as const,
};

export const audienceKeys = {
  all: (identityGeneration: string) =>
    ["attendance-audience", identityGeneration] as const,
  review: (identityGeneration: string, momentID: string) =>
    ["attendance-audience", identityGeneration, momentID] as const,
};

export const looseItemKeys = {
  all: (identityGeneration: string) =>
    ["loose-items", identityGeneration] as const,
  details: (identityGeneration: string) =>
    ["loose-item", identityGeneration] as const,
  detail: (identityGeneration: string, looseItemID: string) =>
    ["loose-item", identityGeneration, looseItemID] as const,
  audiences: (identityGeneration: string) =>
    ["loose-audience", identityGeneration] as const,
  audience: (identityGeneration: string, looseItemID: string) =>
    ["loose-audience", identityGeneration, looseItemID] as const,
  previewRecipientsRoot: (identityGeneration: string) =>
    ["loose-preview-recipients", identityGeneration] as const,
  previewRecipients: (identityGeneration: string, looseItemID: string) =>
    ["loose-preview-recipients", identityGeneration, looseItemID] as const,
  recipientPreviews: (identityGeneration: string) =>
    ["loose-preview", identityGeneration] as const,
  recipientPreview: (
    identityGeneration: string,
    looseItemID: string,
    version: number | undefined,
    recipientID: string,
  ) =>
    [
      "loose-preview",
      identityGeneration,
      looseItemID,
      version,
      recipientID,
    ] as const,
};
