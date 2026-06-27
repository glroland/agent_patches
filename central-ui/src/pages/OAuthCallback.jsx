import { useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { handleCallback } from '../auth/oauth.js';
import { useAuth } from '../auth/AuthContext.jsx';

export default function OAuthCallback() {
  const [error]         = useState(null);
  const [searchParams]  = useSearchParams();
  const { setToken }    = useAuth();
  const navigate        = useNavigate();
  const ran             = useRef(false);

  useEffect(() => {
    // StrictMode double-invoke guard.
    if (ran.current) return;
    ran.current = true;

    handleCallback(searchParams)
      .then(({ access_token, returnUrl }) => {
        setToken(access_token);
        navigate(returnUrl, { replace: true });
      })
      .catch((err) => {
        // Render error inline; don't throw (avoids React error boundary noise).
        console.error('OAuth callback error:', err);
        // Replace state so error shows instead of blank screen.
        navigate(`/login?error=${encodeURIComponent(err.message)}`, { replace: true });
      });
  }, []);  // eslint-disable-line react-hooks/exhaustive-deps

  if (error) {
    return (
      <div className="flex items-center justify-center h-screen text-red-600">
        Login failed: {error}
      </div>
    );
  }

  return (
    <div className="flex items-center justify-center h-screen text-slate-500 text-sm">
      Completing login&hellip;
    </div>
  );
}
