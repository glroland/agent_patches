import { useEffect } from 'react';
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth }    from '../auth/AuthContext.jsx';
import { startLogin } from '../auth/oauth.js';

export default function Login() {
  const { token, oauthConfig, loading } = useAuth();
  const [searchParams] = useSearchParams();
  const location = useLocation();
  const navigate = useNavigate();

  const error   = searchParams.get('error');
  const from    = location.state?.from || '/';

  // Already authenticated — send to the intended destination.
  useEffect(() => {
    if (!loading && token) navigate(from, { replace: true });
  }, [loading, token, from, navigate]);

  const handleLogin = async () => {
    if (!oauthConfig) return;
    await startLogin(oauthConfig, from);
  };

  if (loading) {
    return (
      <div className="min-h-screen bg-slate-50 flex items-center justify-center">
        <span className="text-slate-400 text-sm">Loading…</span>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-slate-50 flex flex-col items-center justify-center p-4">
      <div className="w-full max-w-sm bg-white rounded-2xl shadow-sm border border-slate-200 p-8">

        {/* Branding */}
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-14 h-14 rounded-xl bg-blue-600 text-white text-xl font-bold mb-4 select-none">
            AP
          </div>
          <h1 className="text-xl font-semibold text-slate-800">Agent Patches</h1>
          <p className="text-sm text-slate-500 mt-1">Fleet management console</p>
        </div>

        {/* Error banner */}
        {error && (
          <div className="mb-5 px-4 py-3 rounded-lg bg-red-50 border border-red-100 text-red-700 text-sm leading-snug">
            {error}
          </div>
        )}

        {/* Login button */}
        <button
          onClick={handleLogin}
          disabled={!oauthConfig}
          className="w-full flex items-center justify-center gap-2 px-4 py-2.5 rounded-lg bg-blue-600 text-white font-medium text-sm hover:bg-blue-700 active:bg-blue-800 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
        >
          Sign in with OpenShift
        </button>

        {!oauthConfig && (
          <p className="mt-3 text-center text-xs text-slate-400">
            Connecting to authentication service…
          </p>
        )}
      </div>
    </div>
  );
}
