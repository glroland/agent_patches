export default function StatCard({ label, value, hint, tone = 'default', icon }) {
  const toneStyles = {
    default: 'text-slate-100',
    danger: 'text-rose-400',
    warning: 'text-amber-400',
    success: 'text-emerald-400',
  };

  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-5 shadow-sm shadow-black/20">
      <div className="flex items-center justify-between">
        <p className="text-xs font-medium uppercase tracking-wide text-slate-500">{label}</p>
        {icon && <div className="text-slate-500">{icon}</div>}
      </div>
      <p className={`mt-2 text-3xl font-semibold ${toneStyles[tone]}`}>{value}</p>
      {hint && <p className="mt-1 text-xs text-slate-500">{hint}</p>}
    </div>
  );
}
