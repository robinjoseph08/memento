import { useFavorite } from "../hooks/queries/favorites";
import { isUnavailableResponse } from "./mediaPresentation";
import { LibraryError } from "./presentation";

export function FavoriteControl({
  csrfToken,
  mediaID,
  unavailableMedia,
  onUnavailable,
}: {
  csrfToken: string;
  mediaID: string;
  unavailableMedia: boolean;
  onUnavailable: (error: unknown) => void;
}) {
  const { favorite, toggle } = useFavorite(csrfToken, mediaID, onUnavailable);

  return (
    <section aria-labelledby="favorite-title" className="viewer-favorite">
      <h3 id="favorite-title">Favorite</h3>
      <button
        aria-pressed={favorite.data?.favorite ?? false}
        disabled={unavailableMedia || favorite.isPending || toggle.isPending}
        onClick={() => toggle.mutate(!favorite.data?.favorite)}
        type="button"
      >
        {favorite.data?.favorite ? "Remove Favorite" : "Add Favorite"}
      </button>
      <p>Favorites aren&apos;t shared with other recipients.</p>
      <LibraryError
        error={
          unavailableMedia ||
          isUnavailableResponse(favorite.error ?? toggle.error)
            ? null
            : favorite.error instanceof Error
              ? favorite.error
              : toggle.error instanceof Error
                ? toggle.error
                : null
        }
      />
    </section>
  );
}
