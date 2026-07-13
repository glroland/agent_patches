import { useState, useEffect, useRef } from 'react';
import { Link } from 'react-router-dom';
import Card from '../components/Card';
import Badge from '../components/Badge';
import StatCard from '../components/StatCard';
import TimelineEntry from '../components/TimelineEntry';
import AsyncState from '../components/AsyncState';
import { useFleetSocket } from '../hooks/useFleetSocket';
import { fetchGatewayStats, fetchGatewayPending } from '../api/client';
import { relativeTime } from '../utils/time';

// ─── Gateway stats hook ──────────────────────────────────────────────────────

const POLL_INTERVAL = 5000;
const HISTORY_LEN = 60;

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

// ─── SVG / chart helpers ─────────────────────────────────────────────────────

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

// ─── Formatters ──────────────────────────────────────────────────────────────

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

function fmtMs(ms) {
  if (ms == null) return '—';
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
  return `${Math.round(ms)}ms`;
}

function computeRates(history, key) {
  if (history.length < 2) return [];
  return history.slice(1).map((curr, i) => {
    const prev = history[i];
    const dtMin = (curr.ts - prev.ts) / 60_000;
    return dtMin > 0 ? Math.max(0, (curr[key] - prev[key]) / dtMin) : 0;
  });
}

function patchTone(lastPatchedAt) {
  if (!lastPatchedAt) return 'text-navy-500';
  const age = Date.now() - new Date(lastPatchedAt).getTime();
  if (age > 30 * 24 * 60 * 60 * 1000) return 'text-rose-700';
  if (age > 7 * 24 * 60 * 60 * 1000) return 'text-amber-700';
  return 'text-emerald-700';
}

// ─── Sub-tab: Token Consumption ───────────────────────────────────────────────

function TokenConsumptionTab({ onPendingClick }) {
  const { stats, history, error, lastUpdated } = useGatewayStats();

  const endpoints = stats?.endpoints ?? [];
  const sorted = [...endpoints].sort((a, b) => b.tokens_last_hour - a.tokens_last_hour);

  const responsibilities = stats?.responsibilities ?? [];
  const sortedResp = [...responsibilities].sort((a, b) => b.tokens_last_hour - a.tokens_last_hour);
  const respTokensHr  = responsibilities.reduce((s, r) => s + r.tokens_last_hour, 0);
  const respTokensDay = responsibilities.reduce((s, r) => s + r.tokens_last_day, 0);
  const respReqHr     = responsibilities.reduce((s, r) => s + r.requests_last_hour, 0);
  const respReqDay    = responsibilities.reduce((s, r) => s + r.requests_last_day, 0);
  const respTokens    = responsibilities.reduce((s, r) => s + r.tokens_total, 0);
  const respReq       = responsibilities.reduce((s, r) => s + r.requests_total, 0);

  const totalTokensHr  = endpoints.reduce((s, ep) => s + ep.tokens_last_hour, 0);
  const totalTokensDay = endpoints.reduce((s, ep) => s + ep.tokens_last_day, 0);
  const totalReqHr     = endpoints.reduce((s, ep) => s + ep.requests_last_hour, 0);
  const totalReqDay    = endpoints.reduce((s, ep) => s + ep.requests_last_day, 0);
  const totalTokens    = endpoints.reduce((s, ep) => s + ep.tokens_total, 0);
  const totalReq       = endpoints.reduce((s, ep) => s + ep.requests_total, 0);
  const totalPending   = endpoints.reduce((s, ep) => s + Number(ep.pending_requests), 0);

  const connErrorsHr = (stats?.incoming_conn_errors_last_hour ?? 0) + (stats?.outgoing_conn_errors_last_hour ?? 0);

  const tokenRates   = computeRates(history, 'sumTokens');
  const requestRates = computeRates(history, 'sumRequests');

  const maxTokHr   = Math.max(...endpoints.map((ep) => ep.tokens_last_hour), 1);
  const maxPending = Math.max(...endpoints.map((ep) => Number(ep.pending_requests)), 1);

  const queuePressure = stats?.queue_capacity > 0
    ? stats.queued_requests / stats.queue_capacity
    : 0;

  return (
    <div className="space-y-7">
      {/* Status bar */}
      <div className="flex items-center justify-between">
        {lastUpdated ? (
          <p className="text-xs text-navy-400">Updated {lastUpdated.toLocaleTimeString()} · refreshes every 5 s</p>
        ) : (
          <p className="text-xs text-navy-400">Loading…</p>
        )}
        {error && (
          <div className="max-w-sm rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-xs text-rose-700">
            {/503|configured/i.test(error.message)
              ? 'Gateway stats unavailable — set GATEWAY_STATS_URL on central-backend.'
              : `Error: ${error.message}`}
          </div>
        )}
      </div>

      {!error && !stats && (
        <div className="flex items-center gap-2 py-4 text-sm text-navy-400">
          <span className="h-2 w-2 animate-pulse rounded-full bg-navy-300" />
          Fetching gateway statistics…
        </div>
      )}

      {!error && stats && (
        <>
          {/* Gateway queue info bar */}
          <div className="flex items-center gap-3 rounded-lg border border-navy-200 bg-navy-50 px-4 py-2.5 text-xs text-navy-500">
            <div className="flex flex-col gap-0.5">
              <span>
                <span className="font-semibold text-navy-700">Intelligence Provider URL</span>{' '}
                <span className="font-mono">{stats.upstream}</span>
              </span>
              {stats.upstream_model && (
                <span>
                  <span className="font-semibold text-navy-700">Model Name</span>{' '}
                  <span className="font-mono">{stats.upstream_model}</span>
                </span>
              )}
            </div>
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

          {/* Headline stat cards */}
          <section>
            <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-widest text-navy-400">
              Overview — All Endpoints
            </h2>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
              <StatCard
                label="Pending"
                value={totalPending}
                hint="queued + active · click to inspect"
                tone={totalPending > 10 ? 'warning' : 'default'}
                onClick={onPendingClick}
              />
              <StatCard
                label="Avg Request Duration"
                value={fmtMs(stats.avg_total_duration_ms_last_hour)}
                hint={`wait ${fmtMs(stats.avg_wait_duration_ms_last_hour)} · inference ${fmtMs(stats.avg_inference_duration_ms_last_hour)}`}
              />
              <StatCard
                label="Conn Errors / hr"
                value={fmtNum(connErrorsHr)}
                hint={`${fmtNum(stats.incoming_conn_errors_last_hour)} in · ${fmtNum(stats.outgoing_conn_errors_last_hour)} out`}
                tone={connErrorsHr > 0 ? 'warning' : 'default'}
              />
              <StatCard label="Tokens / hr"   value={fmtNum(totalTokensHr)}  hint={`${fmtNum(totalReqHr)} requests`} />
              <StatCard label="Tokens / day"  value={fmtNum(totalTokensDay)} hint={`${fmtNum(totalReqDay)} requests`} />
              <StatCard label="Avg tok / req" value={fmtAvg(totalTokens, totalReq)} hint="lifetime" />
            </div>
          </section>

          {/* Rate charts */}
          <section>
            <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-widest text-navy-400">
              Live Activity — 5-Minute Window
            </h2>
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
              <RateChart title="Token Rate"   values={tokenRates}   color="#6366f1" gradId="tok-rate" unit="tok / min" />
              <RateChart title="Request Rate" values={requestRates} color="#0ea5e9" gradId="req-rate" unit="req / min" />
            </div>
          </section>

          {/* Tokens / hr by endpoint */}
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

          {/* Per-endpoint metrics table */}
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

          {/* Per-responsibility metrics table */}
          {responsibilities.length > 0 && (
            <section>
              <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-widest text-navy-400">
                Per-Responsibility Metrics
              </h2>
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
                      {sortedResp.map((r) => (
                        <tr key={r.name} className="transition-colors hover:bg-navy-50/60">
                          <td className="px-4 py-3">
                            <span className="rounded-full bg-violet-100 px-2.5 py-0.5 text-xs font-medium text-violet-700">
                              {r.name}
                            </span>
                          </td>
                          <td className="px-4 py-3 text-right font-medium text-indigo-700">{fmtNum(r.tokens_last_hour)}</td>
                          <td className="px-4 py-3 text-right text-navy-600">{fmtNum(r.tokens_last_day)}</td>
                          <td className="px-4 py-3 text-right text-sky-600">{fmtNum(r.requests_last_hour)}</td>
                          <td className="px-4 py-3 text-right text-navy-500">{fmtNum(r.requests_last_day)}</td>
                          <td className="px-4 py-3 text-right text-navy-500">{fmtAvg(r.tokens_total, r.requests_total)}</td>
                          <td className="px-4 py-3 text-right font-semibold text-indigo-800">{fmtNum(r.tokens_total)}</td>
                          <td className="px-4 py-3 text-right text-navy-500">{fmtNum(r.requests_total)}</td>
                          <td className="px-4 py-3 text-right text-xs text-navy-400">{fmtAge(r.last_seen)}</td>
                        </tr>
                      ))}
                    </tbody>
                    <tfoot className="border-t-2 border-navy-200 bg-navy-50">
                      <tr>
                        <td className="px-4 py-2.5 text-xs font-bold text-navy-700">Totals</td>
                        <td className="px-4 py-2.5 text-right text-xs font-bold text-indigo-700">{fmtNum(respTokensHr)}</td>
                        <td className="px-4 py-2.5 text-right text-xs font-bold text-navy-600">{fmtNum(respTokensDay)}</td>
                        <td className="px-4 py-2.5 text-right text-xs font-bold text-sky-600">{fmtNum(respReqHr)}</td>
                        <td className="px-4 py-2.5 text-right text-xs font-bold text-navy-600">{fmtNum(respReqDay)}</td>
                        <td className="px-4 py-2.5 text-right text-xs font-bold text-navy-600">{fmtAvg(respTokens, respReq)}</td>
                        <td className="px-4 py-2.5 text-right text-xs font-bold text-indigo-800">{fmtNum(respTokens)}</td>
                        <td className="px-4 py-2.5 text-right text-xs font-bold text-navy-600">{fmtNum(respReq)}</td>
                        <td />
                      </tr>
                    </tfoot>
                  </table>
                </div>
              </div>
            </section>
          )}

          {/* Live pending breakdown */}
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
        </>
      )}
    </div>
  );
}

// ─── Sub-tab: Status ─────────────────────────────────────────────────────────

function StatusTab({ dashboard, agents }) {
  const { attention } = dashboard;

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <Card title="Needs your attention" subtitle="Agents flagging a problem">
        {attention.length === 0 ? (
          <p className="text-sm text-navy-500">All agents are reporting normal status.</p>
        ) : (
          <div className="space-y-3">
            {attention.map((agent) => (
              <Link key={agent.id} to={`/agents/${agent.id}`} className="block rounded-lg border border-navy-200 p-3 transition-colors hover:border-amber-400">
                <div className="flex items-center justify-between">
                  <p className="text-sm font-medium text-black">{agent.hostname}</p>
                  <Badge variant={agent.status}>{agent.statusLabel}</Badge>
                </div>
                <p className="mt-1 text-xs text-navy-500">
                  {agent.currentTask ?? 'Last seen ' + relativeTime(agent.lastPoll)}
                </p>
              </Link>
            ))}
          </div>
        )}
      </Card>

      <Card title="Patch status" subtitle="Last time updates were applied per agent">
        {!agents || agents.length === 0 ? (
          <p className="text-sm text-navy-500">No agents enrolled.</p>
        ) : (
          <div className="space-y-2">
            {[...agents]
              .sort((a, b) => {
                if (!a.lastPatchedAt && !b.lastPatchedAt) return 0;
                if (!a.lastPatchedAt) return -1;
                if (!b.lastPatchedAt) return 1;
                return new Date(a.lastPatchedAt) - new Date(b.lastPatchedAt);
              })
              .map((agent) => {
                const tone = patchTone(agent.lastPatchedAt);
                return (
                  <Link
                    key={agent.id}
                    to={`/agents/${agent.id}`}
                    className="flex items-center justify-between rounded-lg border border-navy-200 px-3 py-2 transition-colors hover:border-navy-300"
                  >
                    <p className="text-sm text-navy-900 truncate">{agent.displayName || agent.hostname}</p>
                    <p className={`shrink-0 pl-3 text-xs font-medium ${tone}`}>
                      {agent.lastPatchedAt ? relativeTime(agent.lastPatchedAt) : 'Never'}
                    </p>
                  </Link>
                );
              })}
          </div>
        )}
      </Card>
    </div>
  );
}

// ─── Sub-tab: Activity Feed ───────────────────────────────────────────────────

function ActivityFeedTab({ activity }) {
  return (
    <Card title="Live activity feed" subtitle="Recent observations, actions, and recommendations across the fleet">
      {activity.length === 0 ? (
        <p className="text-sm text-navy-500">No recent activity.</p>
      ) : (
        <div className="space-y-5">
          {activity.map((entry) => (
            <TimelineEntry key={entry.id} entry={entry} showAgent />
          ))}
        </div>
      )}
    </Card>
  );
}

// ─── Sub-tab: Pending LLM Requests ───────────────────────────────────────────

function fmtPendingAge(iso) {
  if (!iso) return '';
  const ms = Date.now() - new Date(iso).getTime();
  if (ms < 60_000) return `${Math.round(ms / 1000)}s`;
  if (ms < 3_600_000) {
    const m = Math.floor(ms / 60_000);
    const s = Math.floor((ms % 60_000) / 1000);
    return `${m}m ${s}s`;
  }
  const h = Math.floor(ms / 3_600_000);
  const m = Math.floor((ms % 3_600_000) / 60_000);
  return `${h}h ${m}m`;
}

function PendingLLMRequestsTab() {
  const [requests, setRequests] = useState([]);
  const [error, setError] = useState(null);
  const [lastUpdated, setLastUpdated] = useState(null);
  const [, tick] = useState(0);

  useEffect(() => {
    let cancelled = false;
    const poll = async () => {
      try {
        const data = await fetchGatewayPending();
        if (cancelled) return;
        setRequests(data.requests ?? []);
        setError(null);
        setLastUpdated(new Date());
      } catch (err) {
        if (!cancelled) setError(err);
      }
    };
    poll();
    const pollId = setInterval(poll, 3000);
    const tickId = setInterval(() => tick(n => n + 1), 1000);
    return () => { cancelled = true; clearInterval(pollId); clearInterval(tickId); };
  }, []);

  if (error) {
    return (
      <div className="rounded-lg border border-rose-200 bg-rose-50 p-4 text-sm text-rose-700">
        {/503|configured/i.test(error.message)
          ? 'Gateway unavailable — set GATEWAY_STATS_URL on central-backend.'
          : `Error: ${error.message}`}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        {lastUpdated ? (
          <p className="text-xs text-navy-400">Updated {lastUpdated.toLocaleTimeString()} · refreshes every 3 s</p>
        ) : (
          <p className="text-xs text-navy-400">Loading…</p>
        )}
        {requests.length > 0 && (
          <span className="rounded-full bg-amber-100 px-2.5 py-0.5 text-xs font-semibold text-amber-800">
            {requests.length} pending
          </span>
        )}
      </div>

      {requests.length === 0 && lastUpdated ? (
        <div className="rounded-xl border border-navy-200 bg-navy-50 p-12 text-center">
          <p className="text-sm text-navy-500">No pending LLM requests.</p>
        </div>
      ) : (
        <div className="space-y-3">
          {requests.map((req) => {
            const age = fmtPendingAge(req.submitted_at);
            const displayName = req.name || req.host;
            const showHost = req.name && req.name !== req.host;
            return (
              <div key={req.id} className="overflow-hidden rounded-xl border border-navy-200 bg-white shadow-sm shadow-black/10">
                {/* Header */}
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b border-navy-100 bg-navy-50/60 px-4 py-2.5">
                  <span className="font-mono text-xs font-semibold text-navy-900">{displayName}</span>
                  {showHost && (
                    <span className="font-mono text-[11px] text-navy-400">{req.host}</span>
                  )}
                  {req.responsibility && (
                    <span className="rounded-full bg-violet-100 px-2.5 py-0.5 text-[11px] font-medium text-violet-700">
                      {req.responsibility}
                    </span>
                  )}
                  {req.priority && (
                    <span className="rounded-full bg-sky-100 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-sky-700">
                      interactive
                    </span>
                  )}
                  <span className="ml-auto shrink-0 text-[11px] text-navy-500">
                    {new Date(req.submitted_at).toLocaleTimeString()}{' '}
                    <span className="text-navy-400">({age})</span>
                  </span>
                </div>
                {/* Prompt body */}
                <div className="px-4 py-3">
                  {req.prompt ? (
                    <p className="whitespace-pre-wrap break-words text-xs leading-relaxed text-navy-700">
                      {req.prompt}
                    </p>
                  ) : (
                    <p className="text-xs italic text-navy-400">No prompt content available.</p>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ─── Page ─────────────────────────────────────────────────────────────────────

const TABS = [
  { id: 'token-consumption', label: 'Token Consumption' },
  { id: 'pending',           label: 'Pending LLM Requests' },
  { id: 'status',            label: 'Status' },
  { id: 'feed',              label: 'Activity Feed' },
];

export default function ActivityFeed() {
  const [activeTab, setActiveTab] = useState('token-consumption');
  const { dashboard, agents } = useFleetSocket();

  const isFleetReady = !!dashboard;

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-2xl font-semibold text-black">Activity</h1>
        <p className="mt-1 text-sm text-navy-500">Fleet status, statistics, and recent activity.</p>
      </div>

      <div className="flex gap-1 border-b border-navy-200">
        {TABS.map((t) => (
          <button
            key={t.id}
            onClick={() => setActiveTab(t.id)}
            className={`relative px-4 py-2.5 text-sm font-medium transition-colors ${
              activeTab === t.id ? 'text-navy-700' : 'text-navy-500 hover:text-navy-900'
            }`}
          >
            {t.label}
            {activeTab === t.id && (
              <span className="absolute inset-x-0 -bottom-px h-0.5 rounded-full bg-navy-600" />
            )}
          </button>
        ))}
      </div>

      {activeTab === 'token-consumption' && (
        <TokenConsumptionTab onPendingClick={() => setActiveTab('pending')} />
      )}

      {activeTab === 'pending' && <PendingLLMRequestsTab />}

      {activeTab === 'status' && (
        isFleetReady
          ? <StatusTab dashboard={dashboard} agents={agents} />
          : <AsyncState loading loadingLabel="Loading fleet status..." />
      )}

      {activeTab === 'feed' && (
        isFleetReady
          ? <ActivityFeedTab activity={dashboard.activity} />
          : <AsyncState loading loadingLabel="Loading activity..." />
      )}
    </div>
  );
}
