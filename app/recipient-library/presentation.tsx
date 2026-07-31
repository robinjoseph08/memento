export function LibraryError({ error }: { error: Error | null }) {
  return error ? (
    <p className="form-error" role="alert">
      {error.message}
    </p>
  ) : null;
}
