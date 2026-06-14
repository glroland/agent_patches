const VARIANTS = {
  online: 'bg-emerald-500/15 text-emerald-400 ring-emerald-500/30',
  degraded: 'bg-amber-500/15 text-amber-400 ring-amber-500/30',
  offline: 'bg-rose-500/15 text-rose-400 ring-rose-500/30',
  critical: 'bg-rose-500/15 text-rose-400 ring-rose-500/30',
  warning: 'bg-amber-500/15 text-amber-400 ring-amber-500/30',
  info: 'bg-sky-500/15 text-sky-400 ring-sky-500/30',
  neutral: 'bg-slate-500/15 text-slate-300 ring-slate-500/30',
  passed: 'bg-emerald-500/15 text-emerald-400 ring-emerald-500/30',
  failed: 'bg-rose-500/15 text-rose-400 ring-rose-500/30',
};

export default function Badge({ variant = 'neutral', children, className = '' }) {
  const style = VARIANTS[variant] || VARIANTS.neutral;
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium ring-1 ring-inset ${style} ${className}`}
    >
      {children}
    </span>
  );
}
