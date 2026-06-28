import { useState, useEffect, useRef } from 'react';
import { fetchGatewayStats } from '../api/client';
import StatCard from '../components/StatCard';

// ─── Data hook ──────────────────────────────────────────────────────────────

const POLL_INTERVAL = 5000;
const HISTORY_LEN = 60; // 60 × 5 s = 5-minute window

function useGatewayStats() {
  const [stats, setStats] = useState(null);
  const [error, setError] = useState(null);
  const [lastUpdated, setLastUpdated] = useState(null);
  const histRef = useRef([]);
  const [history, setHistory] = useState([]);

  useEffect(() => {
    let cancelled = false;
    const poll = async () => {
      try {
        const data = await fetchGatewayStats();
        if (cancelled) return;
        setStats(data);
        setError(null);
        setLastUpdated(new Date());
        const sumTokens = (data.endpoints ?? []).reduce((s, ep) => s + ep.tokens_total, 0);
        const sumRequests = (data.endpoints ?? []).reduce((s, ep) => s + ep.requests_total, 0);
        histRef.current = [
          ...histRef.current.slice(-(HISTORY_LEN - 1)),
          { ts: Date.now(), sumTokens, sumRequests },
        ];
        setHistory([...histRef.current]);
      } catch (err) {
        if (!cancelled) setError(err);
      }
    };
    poll();
    const id = setInterval(poll, POLL_INTERVAL);
    return () => { cancelled = true; clearInterval(id); };
  }, []);

  return { stats, history, error, lastUpdated };
}

// ─── SVG helpers ────────────────────────────────────────────────────────────

function SparkArea({ values, color, gradId, height = 80 }) {
  if (!values || values.length < 2) {
    return (
      <div style={{ height }} className="flex items-center justify-center">
        <span className="text-[11px] text-navy-400">Collecting data…</span>
      </div>
    );
  }
  const W = 600;
  const H = height;
  const P = 3;
  const max = Math.max(...values, 1);
  const xs = (i) => P + (i / (values.length - 1)) * (W - 2 * P);
  const ys = (v) => H - P - (v / max) * (H - 2 * P);
  const pts = values.map((v, i) => [xs(i), ys(v)]);
  const line = pts.map(([x, y], i) => `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`).join(' ');
  const area = `${line} L${xs(values.length - 1).toFixed(1)},${H} L${xs(0).toFixed(1)},${H} Z`;
  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="w-full" style={{ height }}>
      <defs>
        <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.28" />
          <stop offset="100%" stopColor={color} stopOpacity="0.02" />
        </linearGradient>
      </defs>
      <path d={area} fill={`url(#${gradId})`} />
      <path d={line} fill="none" stroke={color} strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}

function RateChart({ title, values, color, gradId, unit }) {
  const latest = values.at(-1) ?? 0;
  const peak = values.length ? Math.max(...values) : 0;
  const avg = values.length ? values.reduce((s, v) => s + v, 0) / values.length : 0;
  return (
    <div className="rounded-xl border border-navy-200 bg-white p-4 shadow-sm shadow-black/20">
      <div className="mb-2 flex items-baseline justify-between">
        <span className="text-xs font-semibold uppercase tracking-wide text-navy-500">{title}</span>
        <span className="text-base font-bold text-navy-900">
          {latest.toFixed(1)}{' '}
          <span className="text-xs font-normal text-navy-400">{unit}</span>
        </span>
      </div>
      <SparkArea values={values} color={color} gradId={gradId} height={80} />
      <div className="mt-2 flex justify-between text-[10px] text-navy-400">
        <span>5 min ago</span>
        <span className="text-center">avg {avg.toFixed(1)} · peak {peak.toFixed(1)} {unit}</span>
        <span>now</span>
      </div>
    </div>
  );
}

function HBar({ value, max, color }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  return (
    <div className="h-2 w-full overflow-hidden rounded-full bg-navy-100">
      <div
        className="h-full rounded-full transition-all duration-700 ease-out"
        style={{ width: `${pct}%`, backgroundColor: color }}
      />
    </div>
  );
}

// ─── Formatters ─────────────────────────────────────────────────────────────

function fmtNum(n) {
  if (n == null) return '—';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return n.toLocaleString();
}

function fmtAge(iso) {
  if (!iso) return '—';
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 60_000) return 'just now';
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  return `${Math.floor(diff / 86_400_000)}d ago`;
}

function fmtAvg(tokens, requests) {
  if (!requests) return '—';
  const avg = tokens / requests;
  return avg >= 1000 ? `${(avg / 1000).toFixed(1)}k` : avg.toFixed(0);
}

function computeRates(history, key) {
  if (history.length < 2) return [];
  return history.slice(1).map((curr, i) => {
    const prev = history[i];
    const dtMin = (curr.ts - prev.ts) / 60_000;
    return dtMin > 0 ? Math.max(0, (curr[key] - prev[key]) / dtMin) : 0;
  });
}

// ─── Token Usage tab ────────────────────────────────────────────────────────

function TokenUsageTab({ stats, history }) {
  const endpoints = stats.endpoints ?? [];
  const sorted = [...endpoints].sort((a, b) => b.tokens_last_hour - a.tokens_last_hour);

  const totalTokensHr  = endpoints.reduce((s, ep) => s + ep.tokens_last_hour, 0);
  const totalTokensDay = endpoints.reduce((s, ep) => s + ep.tokens_last_day, 0);
  const totalReqHr     = endpoints.reduce((s, ep) => s + ep.requests_last_hour, 0);
  const totalReqDay    = endpoints.reduce((s, ep) => s + ep.requests_last_day, 0);
  const totalTokens    = endpoints.reduce((s, ep) => s + ep.tokens_total, 0);
  const totalReq       = endpoints.reduce((s, ep) => s + ep.requests_total, 0);
  const totalPending   = endpoints.reduce((s, ep) => s + Number(ep.pending_requests), 0);

  const tokenRates   = computeRates(history, 'sumTokens');
  const requestRates = computeRates(history, 'sumRequests');

  const maxTokHr  = Math.max(...endpoints.map((ep) => ep.tokens_last_hour), 1);
  const maxPending = Math.max(...endpoints.map((ep) => Number(ep.pending_requests)), 1);

  const queuePressure = stats.queue_capacity > 0
    ? stats.queued_requests / stats.queue_capacity
    : 0;

  return (
    <div className="space-y-7">

      {/* ── Gateway queue info bar ── */}
      <div className="flex items-center gap-3 rounded-lg border border-navy-200 bg-navy-50 px-4 py-2.5 text-xs text-navy-500">
        <span className="font-semibold text-navy-700">Upstream</span>
        <span className="font-mono">{stats.upstream}</span>
        <span className="ml-auto flex items-center gap-4">
          <span>
            Queue{' '}
            <span className={`font-semibold ${queuePressure > 0.8 ? 'text-rose-600' : 'text-navy-700'}`}>
              {stats.queued_requests}/{stats.queue_capacity}
            </span>
          </span>
          <span>
            Workers{' '}
            <span className="font-semibold text-navy-700">
              {stats.active_requests}/{stats.max_concurrency}
            </span>
          </span>
        </span>
      </div>

      {/* ── Headline stat cards ── */}
      <section>
        <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-widest text-navy-400">
          Overview — All Endpoints
        </h2>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
          <StatCard
            label="Pending"
            value={totalPending}
            hint="queued + active"
            tone={totalPending > 10 ? 'warning' : 'default'}
          />
          <StatCard label="Tokens / hr"  value={fmtNum(totalTokensHr)}  hint={`${fmtNum(totalReqHr)} requests`} />
          <StatCard label="Tokens / day" value={fmtNum(totalTokensDay)} hint={`${fmtNum(totalReqDay)} requests`} />
          <StatCard label="Avg tok / req" value={fmtAvg(totalTokens, totalReq)} hint="lifetime" />
          <StatCard
            label="Endpoints"
            value={endpoints.length}
            hint={`${endpoints.filter((ep) => Number(ep.pending_requests) > 0).length} active`}
          />
        </div>
      </section>

      {/* ── Rate charts ── */}
      <section>
        <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-widest text-navy-400">
          Live Activity — 5-Minute Window
        </h2>
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          <RateChart
            title="Token Rate"
            values={tokenRates}
            color="#6366f1"
            gradId="tok-rate"
            unit="tok / min"
          />
          <RateChart
            title="Request Rate"
            values={requestRates}
            color="#0ea5e9"
            gradId="req-rate"
            unit="req / min"
          />
        </div>
      </section>

      {/* ── Token/hr by endpoint horizontal bars ── */}
      {endpoints.length > 0 && (
        <section>
          <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-widest text-navy-400">
            Tokens / Hour by Endpoint
          </h2>
          <div className="rounded-xl border border-navy-200 bg-white p-5 shadow-sm shadow-black/20 space-y-4">
            {sorted.map((ep) => (
              <div key={ep.host}>
                <div className="mb-1.5 flex items-center justify-between text-xs">
                  <span className="font-mono text-navy-700 truncate">{ep.name || ep.host}</span>
                  <div className="ml-4 flex shrink-0 items-center gap-3">
                    <span className="text-navy-400">avg {fmtAvg(ep.tokens_total, ep.requests_total)} tok/req</span>
                    <span className="font-semibold text-indigo-700">{fmtNum(ep.tokens_last_hour)} tok</span>
                  </div>
                </div>
                <HBar value={ep.tokens_last_hour} max={maxTokHr} color="#6366f1" />
              </div>
            ))}
          </div>
        </section>
      )}

      {/* ── Per-endpoint metrics table ── */}
      {endpoints.length > 0 ? (
        <section>
          <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-widest text-navy-400">
            Per-Endpoint Metrics
          </h2>
          <div className="overflow-hidden rounded-xl border border-navy-200 bg-white shadow-sm shadow-black/20">
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-navy-200 bg-navy-50">
                    {[
                      ['Endpoint', 'left'],
                      ['Pending', 'right'],
                      ['Tok / hr', 'right'],
                      ['Tok / day', 'right'],
                      ['Req / hr', 'right'],
                      ['Req / day', 'right'],
                      ['Avg tok / req', 'right'],
                      ['Total Tokens', 'right'],
                      ['Total Req', 'right'],
                      ['Last Seen', 'right'],
                    ].map(([h, align]) => (
                      <th
                        key={h}
                        className={`px-4 py-3 text-${align} text-[10px] font-semibold uppercase tracking-widest text-navy-400`}
                      >
                        {h}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-navy-100">
                  {sorted.map((ep) => {
                    const pending = Number(ep.pending_requests);
                    return (
                      <tr key={ep.host} className="transition-colors hover:bg-navy-50/60">
                        <td className="px-4 py-3 font-mono text-xs text-navy-800">{ep.name || ep.host}</td>
                        <td className="px-4 py-3 text-right">
                          {pending > 0 ? (
                            <span className="inline-flex min-w-[22px] items-center justify-center rounded-full bg-amber-100 px-2 py-0.5 text-xs font-semibold text-amber-800">
                              {pending}
                            </span>
                          ) : (
                            <span className="text-xs text-navy-300">—</span>
                          )}
                        </td>
                        <td className="px-4 py-3 text-right font-medium text-indigo-700">{fmtNum(ep.tokens_last_hour)}</td>
                        <td className="px-4 py-3 text-right text-navy-600">{fmtNum(ep.tokens_last_day)}</td>
                        <td className="px-4 py-3 text-right text-sky-600">{fmtNum(ep.requests_last_hour)}</td>
                        <td className="px-4 py-3 text-right text-navy-500">{fmtNum(ep.requests_last_day)}</td>
                        <td className="px-4 py-3 text-right text-navy-500">{fmtAvg(ep.tokens_total, ep.requests_total)}</td>
                        <td className="px-4 py-3 text-right font-semibold text-indigo-800">{fmtNum(ep.tokens_total)}</td>
                        <td className="px-4 py-3 text-right text-navy-500">{fmtNum(ep.requests_total)}</td>
                        <td className="px-4 py-3 text-right text-xs text-navy-400">{fmtAge(ep.last_seen)}</td>
                      </tr>
                    );
                  })}
                </tbody>
                <tfoot className="border-t-2 border-navy-200 bg-navy-50">
                  <tr>
                    <td className="px-4 py-2.5 text-xs font-bold text-navy-700">Totals</td>
                    <td className="px-4 py-2.5 text-right">
                      {totalPending > 0 ? (
                        <span className="inline-flex min-w-[22px] items-center justify-center rounded-full bg-amber-100 px-2 py-0.5 text-xs font-bold text-amber-800">
                          {totalPending}
                        </span>
                      ) : (
                        <span className="text-xs text-navy-300">—</span>
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-right text-xs font-bold text-indigo-700">{fmtNum(totalTokensHr)}</td>
                    <td className="px-4 py-2.5 text-right text-xs font-bold text-navy-600">{fmtNum(totalTokensDay)}</td>
                    <td className="px-4 py-2.5 text-right text-xs font-bold text-sky-600">{fmtNum(totalReqHr)}</td>
                    <td className="px-4 py-2.5 text-right text-xs font-bold text-navy-600">{fmtNum(totalReqDay)}</td>
                    <td className="px-4 py-2.5 text-right text-xs font-bold text-navy-600">{fmtAvg(totalTokens, totalReq)}</td>
                    <td className="px-4 py-2.5 text-right text-xs font-bold text-indigo-800">{fmtNum(totalTokens)}</td>
                    <td className="px-4 py-2.5 text-right text-xs font-bold text-navy-600">{fmtNum(totalReq)}</td>
                    <td />
                  </tr>
                </tfoot>
              </table>
            </div>
          </div>
        </section>
      ) : (
        <div className="rounded-xl border border-navy-200 bg-navy-50 p-8 text-center">
          <p className="text-sm text-navy-500">No endpoint data yet — traffic will appear here once agents start sending requests through the gateway.</p>
        </div>
      )}

      {/* ── Token usage by responsibility ── */}
      {(stats.responsibilities ?? []).length > 0 && (
        <section>
          <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-widest text-navy-400">
            Token Usage by Responsibility
          </h2>

          {/* Bar chart */}
          <div className="mb-4 rounded-xl border border-navy-200 bg-white p-5 shadow-sm shadow-black/20 space-y-4">
            {[...(stats.responsibilities ?? [])]
              .sort((a, b) => b.tokens_last_hour - a.tokens_last_hour)
              .map((r) => {
                const maxRespTokHr = Math.max(
                  ...(stats.responsibilities ?? []).map((x) => x.tokens_last_hour),
                  1,
                );
                return (
                  <div key={r.name}>
                    <div className="mb-1.5 flex items-center justify-between text-xs">
                      <span className="font-mono text-navy-700 truncate">{r.name}</span>
                      <div className="ml-4 flex shrink-0 items-center gap-3">
                        <span className="text-navy-400">avg {fmtAvg(r.tokens_total, r.requests_total)} tok/req</span>
                        <span className="font-semibold text-violet-700">{fmtNum(r.tokens_last_hour)} tok/hr</span>
                      </div>
                    </div>
                    <HBar value={r.tokens_last_hour} max={maxRespTokHr} color="#7c3aed" />
                  </div>
                );
              })}
          </div>

          {/* Table */}
          <div className="overflow-hidden rounded-xl border border-navy-200 bg-white shadow-sm shadow-black/20">
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-navy-200 bg-navy-50">
                    {[
                      ['Responsibility', 'left'],
                      ['Tok / hr', 'right'],
                      ['Tok / day', 'right'],
                      ['Req / hr', 'right'],
                      ['Req / day', 'right'],
                      ['Avg tok / req', 'right'],
                      ['Total Tokens', 'right'],
                      ['Total Req', 'right'],
                      ['Last Seen', 'right'],
                    ].map(([h, align]) => (
                      <th
                        key={h}
                        className={`px-4 py-3 text-${align} text-[10px] font-semibold uppercase tracking-widest text-navy-400`}
                      >
                        {h}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-navy-100">
                  {[...(stats.responsibilities ?? [])]
                    .sort((a, b) => b.tokens_last_hour - a.tokens_last_hour)
                    .map((r) => (
                      <tr key={r.name} className="transition-colors hover:bg-navy-50/60">
                        <td className="px-4 py-3 font-mono text-xs text-navy-800">{r.name}</td>
                        <td className="px-4 py-3 text-right font-medium text-violet-700">{fmtNum(r.tokens_last_hour)}</td>
                        <td className="px-4 py-3 text-right text-navy-600">{fmtNum(r.tokens_last_day)}</td>
                        <td className="px-4 py-3 text-right text-sky-600">{fmtNum(r.requests_last_hour)}</td>
                        <td className="px-4 py-3 text-right text-navy-500">{fmtNum(r.requests_last_day)}</td>
                        <td className="px-4 py-3 text-right text-navy-500">{fmtAvg(r.tokens_total, r.requests_total)}</td>
                        <td className="px-4 py-3 text-right font-semibold text-violet-800">{fmtNum(r.tokens_total)}</td>
                        <td className="px-4 py-3 text-right text-navy-500">{fmtNum(r.requests_total)}</td>
                        <td className="px-4 py-3 text-right text-xs text-navy-400">{fmtAge(r.last_seen)}</td>
                      </tr>
                    ))}
                </tbody>
                <tfoot className="border-t-2 border-navy-200 bg-navy-50">
                  <tr>
                    <td className="px-4 py-2.5 text-xs font-bold text-navy-700">Totals</td>
                    <td className="px-4 py-2.5 text-right text-xs font-bold text-violet-700">
                      {fmtNum((stats.responsibilities ?? []).reduce((s, r) => s + r.tokens_last_hour, 0))}
                    </td>
                    <td className="px-4 py-2.5 text-right text-xs font-bold text-navy-600">
                      {fmtNum((stats.responsibilities ?? []).reduce((s, r) => s + r.tokens_last_day, 0))}
                    </td>
                    <td className="px-4 py-2.5 text-right text-xs font-bold text-sky-600">
                      {fmtNum((stats.responsibilities ?? []).reduce((s, r) => s + r.requests_last_hour, 0))}
                    </td>
                    <td className="px-4 py-2.5 text-right text-xs font-bold text-navy-600">
                      {fmtNum((stats.responsibilities ?? []).reduce((s, r) => s + r.requests_last_day, 0))}
                    </td>
                    <td className="px-4 py-2.5 text-right text-xs font-bold text-navy-600">
                      {fmtAvg(
                        (stats.responsibilities ?? []).reduce((s, r) => s + r.tokens_total, 0),
                        (stats.responsibilities ?? []).reduce((s, r) => s + r.requests_total, 0),
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-right text-xs font-bold text-violet-800">
                      {fmtNum((stats.responsibilities ?? []).reduce((s, r) => s + r.tokens_total, 0))}
                    </td>
                    <td className="px-4 py-2.5 text-right text-xs font-bold text-navy-600">
                      {fmtNum((stats.responsibilities ?? []).reduce((s, r) => s + r.requests_total, 0))}
                    </td>
                    <td />
                  </tr>
                </tfoot>
              </table>
            </div>
          </div>
        </section>
      )}

      {/* ── Live pending breakdown ── */}
      {endpoints.some((ep) => Number(ep.pending_requests) > 0) && (
        <section>
          <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-widest text-navy-400">
            Live Pending Requests by Endpoint
          </h2>
          <div className="rounded-xl border border-amber-200 bg-white p-5 shadow-sm shadow-black/20 space-y-4">
            {sorted
              .filter((ep) => Number(ep.pending_requests) > 0)
              .map((ep) => (
                <div key={ep.host}>
                  <div className="mb-1.5 flex items-center justify-between text-xs">
                    <span className="font-mono text-navy-700">{ep.host}</span>
                    <span className="font-bold text-amber-700">{ep.pending_requests}</span>
                  </div>
                  <HBar value={Number(ep.pending_requests)} max={maxPending} color="#f59e0b" />
                </div>
              ))}
          </div>
        </section>
      )}
    </div>
  );
}

// ─── Tab definitions ─────────────────────────────────────────────────────────

const TABS = [
  { id: 'tokens', label: 'Token Usage' },
  // { id: 'queue',  label: 'Queue Health' },   // coming soon
  // { id: 'perf',  label: 'Performance' },     // coming soon
];

// ─── Page ────────────────────────────────────────────────────────────────────

export default function Statistics() {
  const [activeTab, setActiveTab] = useState('tokens');
  const { stats, history, error, lastUpdated } = useGatewayStats();

  return (
    <div className="space-y-5">

      {/* Header */}
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-xl font-semibold text-navy-900">Statistics</h1>
          {lastUpdated ? (
            <p className="mt-0.5 text-xs text-navy-400">
              Updated {lastUpdated.toLocaleTimeString()} · refreshes every 5 s
            </p>
          ) : (
            <p className="mt-0.5 text-xs text-navy-400">Loading…</p>
          )}
        </div>

        {error && (
          <div className="max-w-sm rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-xs text-rose-700">
            {/503|configured/i.test(error.message)
              ? 'Gateway stats unavailable — set GATEWAY_STATS_URL on central-backend.'
              : `Error: ${error.message}`}
          </div>
        )}
      </div>

      {/* Tab bar */}
      <div className="flex border-b border-navy-200">
        {TABS.map((tab) => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={`relative px-4 py-2.5 text-sm font-medium transition-colors focus:outline-none ${
              activeTab === tab.id
                ? 'text-indigo-700'
                : 'text-navy-500 hover:text-navy-800'
            }`}
          >
            {tab.label}
            {activeTab === tab.id && (
              <span className="absolute inset-x-0 bottom-0 h-[2px] rounded-full bg-indigo-600" />
            )}
          </button>
        ))}
      </div>

      {/* Loading shimmer */}
      {!error && !stats && (
        <div className="flex items-center gap-2 py-4 text-sm text-navy-400">
          <span className="h-2 w-2 animate-pulse rounded-full bg-navy-300" />
          Fetching gateway statistics…
        </div>
      )}

      {/* Tab content */}
      {!error && stats && activeTab === 'tokens' && (
        <TokenUsageTab stats={stats} history={history} />
      )}
    </div>
  );
}
