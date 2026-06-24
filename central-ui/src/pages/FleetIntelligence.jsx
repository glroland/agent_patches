import { Link } from 'react-router-dom';
import Card from '../components/Card';
import AsyncState from '../components/AsyncState';
import { useFleetSocket } from '../hooks/useFleetSocket';
import { relativeTime } from '../utils/time';

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

  const { headline, recommendations, generatedAt, agentCount } = intelligence;
  const high   = recommendations.filter((r) => r.priority === 'high');
  const medium = recommendations.filter((r) => r.priority === 'medium');
  const low    = recommendations.filter((r) => r.priority === 'low');

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-black">Fleet Intelligence</h1>
          <p className="mt-1 text-sm text-navy-500">AI-generated analysis of your fleet's health, patterns, and recommended next actions.</p>
        </div>
        <div className="shrink-0 text-right">
          <p className="text-xs text-navy-500">Last analysed {relativeTime(generatedAt)}</p>
          {agentCount && <p className="mt-0.5 text-xs text-navy-400">{agentCount} agent{agentCount !== 1 ? 's' : ''} included</p>}
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
    </div>
  );
}
