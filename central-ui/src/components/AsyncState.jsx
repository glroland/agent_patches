// Shared loading/error placeholder for screens fetching from central-backend.
export default function AsyncState({ loading, error, loadingLabel = 'Loading...' }) {
  if (loading) {
    return <p className="text-sm text-slate-500">{loadingLabel}</p>;
  }

  if (error) {
    return (
      <div className="rounded-lg border border-rose-500/40 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">
        Failed to load data from central-backend: {error.message}
      </div>
    );
  }

  return null;
}
