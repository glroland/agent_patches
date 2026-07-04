import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import Card from '../components/Card';
import AsyncState from '../components/AsyncState';
import { useFleetSocket } from '../hooks/useFleetSocket';
import { refreshIntelligence } from '../api/client';
import { relativeTime } from '../utils/time';

// If no fresh report arrives over the WS within this window, assume the
// analysis failed server-side and re-enable the button.
const REFRESH_FALLBACK_MS = 5 * 60 * 1000;

const PRIORITY_STYLES = {
  high:   { badge: 'bg-rose-100 text-rose-700',  border: 'border-rose-200'  },
  medium: { badge: 'bg-amber-100 text-amber-700', border: 'border-amber-200' },
  low:    { badge: 'bg-navy-100 text-navy-600', border: 'border-navy-200' },
};

const CATEGORY_LABEL = {
  health:        'Health',
  security:      'Security',
  feature:       'New Feature',
  configuration: 'Config',
};

export default function FleetIntelligence() {
  const { intelligence } = useFleetSocket();
  const [refreshing, setRefreshing] = useState(false);
  const [refreshError, setRefreshError] = useState(null);
  const generatedAtOnClick = useRef(null);

  // The refreshed report arrives over the WS; a changed generatedAt means
  // the run we triggered has completed.
  const generatedAt = intelligence?.generatedAt ?? null;
  useEffect(() => {
    if (!refreshing) return;
    if (generatedAt !== generatedAtOnClick.current) {
      setRefreshing(false);
      return;
    }
    const fallback = setTimeout(() => {
      setRefreshing(false);
      setRefreshError('Analysis did not complete — check the central-backend logs.');
    }, REFRESH_FALLBACK_MS);
    return () => clearTimeout(fallback);
  }, [refreshing, generatedAt]);

  async function handleRefresh() {
    setRefreshError(null);
    generatedAtOnClick.current = generatedAt;
    setRefreshing(true);
    try {
      await refreshIntelligence();
    } catch (err) {
      setRefreshing(false);
      setRefreshError(err.message);
    }
  }

  if (intelligence === undefined) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-semibold text-black">Fleet Intelligence</h1>
          <p className="mt-1 text-sm text-navy-500">AI-generated analysis of your fleet's health, patterns, and recommended next actions.</p>
        </div>
        <Card>
          <div className="py-8 text-center space-y-2">
            <p className="text-sm font-medium text-navy-600">Fleet intelligence is not configured.</p>
            <p className="text-xs text-navy-400">Set <code className="rounded bg-navy-100 px-1 py-0.5">INTELLIGENCE_BASE_URL</code> in your central-backend config to enable it.</p>
          </div>
        </Card>
      </div>
    );
  }

  if (intelligence === null) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-semibold text-black">Fleet Intelligence</h1>
          <p className="mt-1 text-sm text-navy-500">AI-generated analysis of your fleet's health, patterns, and recommended next actions.</p>
        </div>
        <AsyncState loading loadingLabel="Analysing your fleet…" />
      </div>
    );
  }

  const { headline, recommendations, resourceOptimization = [], approvalInsights = [], agentCount } = intelligence;
  const high   = recommendations.filter((r) => r.priority === 'high');
  const medium = recommendations.filter((r) => r.priority === 'medium');
  const low    = recommendations.filter((r) => r.priority === 'low');
  const optHigh   = resourceOptimization.filter((r) => r.priority === 'high');
  const optMedium = resourceOptimization.filter((r) => r.priority === 'medium');
  const optLow    = resourceOptimization.filter((r) => r.priority === 'low');
  const aiHigh   = approvalInsights.filter((r) => r.priority === 'high');
  const aiMedium = approvalInsights.filter((r) => r.priority === 'medium');
  const aiLow    = approvalInsights.filter((r) => r.priority === 'low');

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-black">Fleet Intelligence</h1>
          <p className="mt-1 text-sm text-navy-500">AI-generated analysis of your fleet's health, patterns, and recommended next actions.</p>
        </div>
        <div className="flex shrink-0 flex-col items-end gap-2">
          <button
            onClick={handleRefresh}
            disabled={refreshing}
            className="inline-flex items-center gap-1.5 rounded-lg border border-navy-300 px-3 py-1.5 text-xs font-semibold text-navy-700 hover:border-navy-400 hover:text-navy-900 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {refreshing && (
              <svg className="h-3.5 w-3.5 animate-spin" viewBox="0 0 24 24" fill="none">
                <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v4a4 4 0 00-4 4H4z" />
              </svg>
            )}
            {refreshing ? 'Re-analysing…' : 'Re-analyse now'}
          </button>
          <div className="text-right">
            <p className="text-xs text-navy-500">Last analysed {relativeTime(generatedAt)}</p>
            {agentCount && <p className="mt-0.5 text-xs text-navy-400">{agentCount} agent{agentCount !== 1 ? 's' : ''} included</p>}
            {refreshError && <p className="mt-0.5 text-xs text-rose-600">{refreshError}</p>}
          </div>
        </div>
      </div>

      <div className="rounded-xl border border-navy-200 bg-navy-50 px-5 py-4">
        <p className="text-xs font-semibold uppercase tracking-wider text-navy-600 mb-1">Summary</p>
        <p className="text-base font-medium text-black">{headline}</p>
      </div>

      {recommendations.length === 0 ? (
        <Card>
          <p className="py-6 text-center text-sm text-navy-500">No recommendations at this time — fleet looks healthy.</p>
        </Card>
      ) : (
        <div className="space-y-4">
          {[{ label: 'High priority', items: high }, { label: 'Medium priority', items: medium }, { label: 'Low priority', items: low }]
            .filter(({ items }) => items.length > 0)
            .map(({ label, items }) => (
              <div key={label}>
                <p className="mb-2 text-xs font-semibold uppercase tracking-wider text-navy-500">{label}</p>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
                  {items.map((rec, i) => {
                    const styles = PRIORITY_STYLES[rec.priority] ?? PRIORITY_STYLES.low;
                    return (
                      <div key={i} className={`rounded-lg border bg-white p-3.5 ${styles.border}`}>
                        <div className="flex items-center gap-2 mb-2">
                          <span className={`rounded-full px-2 py-0.5 text-xs font-semibold ${styles.badge}`}>
                            {rec.priority}
                          </span>
                          {rec.category && (
                            <span className="text-xs text-navy-500">
                              {CATEGORY_LABEL[rec.category] ?? rec.category}
                            </span>
                          )}
                        </div>
                        <p className="text-sm font-medium text-navy-900 mb-1">{rec.title}</p>
                        <p className="text-xs text-navy-500 leading-relaxed">{rec.body}</p>
                      </div>
                    );
                  })}
                </div>
              </div>
            ))}
        </div>
      )}

      {approvalInsights.length > 0 && (
        <div className="space-y-4">
          <div>
            <h2 className="text-lg font-semibold text-black">Approval Pattern Analysis</h2>
            <p className="mt-0.5 text-sm text-navy-500">Patterns in approval and rejection history with recommendations to improve automation, risk classification, and agent behaviour.</p>
          </div>
          {[{ label: 'High priority', items: aiHigh }, { label: 'Medium priority', items: aiMedium }, { label: 'Low priority', items: aiLow }]
            .filter(({ items }) => items.length > 0)
            .map(({ label, items }) => (
              <div key={label}>
                <p className="mb-2 text-xs font-semibold uppercase tracking-wider text-navy-500">{label}</p>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
                  {items.map((ins, i) => {
                    const styles = PRIORITY_STYLES[ins.priority] ?? PRIORITY_STYLES.low;
                    return (
                      <div key={i} className={`rounded-lg border bg-white p-3.5 ${styles.border}`}>
                        <div className="flex items-center gap-2 mb-2">
                          <span className={`rounded-full px-2 py-0.5 text-xs font-semibold ${styles.badge}`}>
                            {ins.priority}
                          </span>
                          {ins.hostname && ins.hostname !== 'all' && (
                            <span className="text-xs text-navy-400 font-mono">{ins.hostname}</span>
                          )}
                        </div>
                        <p className="text-sm font-medium text-navy-900 mb-1">{ins.pattern}</p>
                        <p className="text-xs font-medium text-navy-700 mb-1">{ins.recommendation}</p>
                        {ins.evidence && (
                          <p className="text-xs text-navy-400 leading-relaxed italic">{ins.evidence}</p>
                        )}
                      </div>
                    );
                  })}
                </div>
              </div>
            ))}
        </div>
      )}

      {resourceOptimization.length > 0 && (
        <div className="space-y-4">
          <div>
            <h2 className="text-lg font-semibold text-black">Resource Optimization</h2>
            <p className="mt-0.5 text-sm text-navy-500">Responsibility schedule and configuration recommendations based on token usage and outcome history.</p>
          </div>
          {[{ label: 'High priority', items: optHigh }, { label: 'Medium priority', items: optMedium }, { label: 'Low priority', items: optLow }]
            .filter(({ items }) => items.length > 0)
            .map(({ label, items }) => (
              <div key={label}>
                <p className="mb-2 text-xs font-semibold uppercase tracking-wider text-navy-500">{label}</p>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
                  {items.map((opt, i) => {
                    const styles = PRIORITY_STYLES[opt.priority] ?? PRIORITY_STYLES.low;
                    return (
                      <div key={i} className={`rounded-lg border bg-white p-3.5 ${styles.border}`}>
                        <div className="flex items-center gap-2 mb-2">
                          <span className={`rounded-full px-2 py-0.5 text-xs font-semibold ${styles.badge}`}>
                            {opt.priority}
                          </span>
                          <span className="text-xs text-navy-500 font-mono">{opt.responsibility}</span>
                        </div>
                        {opt.hostname && opt.hostname !== 'all' && (
                          <p className="text-xs text-navy-400 mb-1">{opt.hostname}</p>
                        )}
                        <div className="flex items-center gap-1.5 mb-2 text-xs text-navy-500">
                          <span className="line-through">{opt.currentSchedule}</span>
                          <span>→</span>
                          <span className="font-medium text-navy-700">{opt.proposedChange}</span>
                        </div>
                        <p className="text-xs text-navy-500 leading-relaxed">{opt.rationale}</p>
                      </div>
                    );
                  })}
                </div>
              </div>
            ))}
        </div>
      )}
    </div>
  );
}
