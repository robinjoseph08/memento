import type { Event } from "../types/generated/events";

export function SourceMetadataSuggestions({
  event,
  onChange,
}: {
  event: Event;
  onChange: (mutator: (next: Event) => void) => void;
}) {
  const suggestions = event.sources.flatMap((source, index) =>
    source.metadata_suggestion
      ? [{ sourceID: source.id, index, suggestion: source.metadata_suggestion }]
      : [],
  );
  if (suggestions.length === 0) return null;

  return (
    <section
      aria-labelledby="source-metadata-suggestions-title"
      className="source-metadata-suggestions"
    >
      <h5 id="source-metadata-suggestions-title">
        Source metadata suggestions
      </h5>
      <p>
        Source changes are advisory. Event metadata changes only when you use a
        suggestion, then follows the normal autosave and conflict workflow.
      </p>
      <ul>
        {suggestions.map(({ sourceID, index, suggestion }) => {
          const suggestedName = suggestion.name;
          const suggestedDescription = suggestion.description;
          const titleUsed = suggestedName === event.title;
          const descriptionUsed = suggestedDescription === event.description;
          const titleValid =
            suggestedName === null || Array.from(suggestedName).length <= 240;
          const descriptionValid =
            suggestedDescription === null ||
            Array.from(suggestedDescription).length <= 2000;
          return (
            <li key={sourceID}>
              <strong>Source {index + 1}</strong>
              {suggestedName !== null ? (
                <div>
                  <span>Suggested title: {suggestedName}</span>
                  <button
                    aria-label={
                      titleUsed
                        ? "Suggested title currently used"
                        : `Use suggested title ${suggestedName}`
                    }
                    disabled={titleUsed || !titleValid}
                    onClick={() =>
                      onChange((next) => {
                        next.title = suggestedName;
                      })
                    }
                    type="button"
                  >
                    {titleUsed ? "Currently used" : "Use suggested title"}
                  </button>
                  {!titleValid ? (
                    <small>
                      Suggested title exceeds the Event title limit.
                    </small>
                  ) : null}
                </div>
              ) : null}
              {suggestedDescription !== null ? (
                <div>
                  <span>
                    Suggested description:{" "}
                    {suggestedDescription || "(empty description)"}
                  </span>
                  <button
                    aria-label={
                      descriptionUsed
                        ? "Suggested description currently used"
                        : "Use suggested description from Source"
                    }
                    disabled={descriptionUsed || !descriptionValid}
                    onClick={() =>
                      onChange((next) => {
                        next.description = suggestedDescription;
                      })
                    }
                    type="button"
                  >
                    {descriptionUsed
                      ? "Currently used"
                      : "Use suggested description"}
                  </button>
                  {!descriptionValid ? (
                    <small>
                      Suggested description exceeds the Event description limit.
                    </small>
                  ) : null}
                </div>
              ) : null}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
