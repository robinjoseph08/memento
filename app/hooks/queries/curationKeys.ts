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
