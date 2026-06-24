// Shared loading/error placeholder for screens fetching from central-backend.
export default function AsyncState({ loading, error, loadingLabel = 'Loading...' }) {
  if (loading) {
    return <p className="text-sm text-navy-500">{loadingLabel}</p>;
  }

  if (error) {
    return (
      <div className="rounded-lg border border-rose-300 bg-rose-50 px-4 py-3 text-sm text-rose-700">
        Failed to load data from central-backend: {error.message}
      </div>
    );
  }

  return null;
}
