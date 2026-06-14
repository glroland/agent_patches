export default function ProgressBar({ pct, label }) {
  const clamped = Math.max(0, Math.min(100, pct));
  let color = 'bg-emerald-500';
  if (clamped >= 90) color = 'bg-rose-500';
  else if (clamped >= 75) color = 'bg-amber-500';

  return (
    <div>
      {label && (
        <div className="mb-1 flex items-center justify-between text-xs text-slate-400">
          <span>{label}</span>
          <span>{clamped.toFixed(1)}%</span>
        </div>
      )}
      <div className="h-2 w-full overflow-hidden rounded-full bg-slate-800">
        <div className={`h-full rounded-full ${color}`} style={{ width: `${clamped}%` }} />
      </div>
    </div>
  );
}
