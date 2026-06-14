import { useEffect, useState } from 'react';

// Calls `fetcher` and tracks its loading/error/data state. `fetcher` is
// re-invoked whenever `deps` changes, mirroring useEffect's dependency rules.
export function useApi(fetcher, deps = []) {
  const [state, setState] = useState({ data: null, loading: true, error: null });

  useEffect(() => {
    let cancelled = false;

    Promise.resolve()
      .then(() => {
        if (cancelled) return undefined;
        setState({ data: null, loading: true, error: null });
        return fetcher();
      })
      .then((result) => {
        if (cancelled || result === undefined) return;
        setState({ data: result, loading: false, error: null });
      })
      .catch((err) => {
        if (cancelled) return;
        setState({ data: null, loading: false, error: err });
      });

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return state;
}
