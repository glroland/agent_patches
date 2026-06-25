export default function StatCard({ label, value, hint, tone = 'default', icon }) {
  const toneStyles = {
    default: 'text-black',
    danger: 'text-rose-700',
    warning: 'text-amber-700',
    success: 'text-emerald-700',
  };

  return (
    <div className="rounded-xl border border-navy-200 bg-white p-3 text-center shadow-sm shadow-black/20">
      <div className="flex items-center justify-center gap-1.5">
        <p className="text-xs font-medium uppercase tracking-wide text-navy-500">{label}</p>
        {icon && <div className="text-navy-500">{icon}</div>}
      </div>
      <p className={`mt-1 text-2xl font-semibold ${toneStyles[tone]}`}>{value}</p>
      {hint && <p className="mt-0.5 text-xs text-navy-500">{hint}</p>}
    </div>
  );
}
