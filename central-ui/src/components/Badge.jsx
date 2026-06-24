const VARIANTS = {
  active: 'bg-navy-100 text-navy-700 ring-navy-300',
  idle: 'bg-navy-100 text-navy-700 ring-navy-200',
  attention: 'bg-amber-100 text-amber-700 ring-amber-300',
  online: 'bg-emerald-100 text-emerald-700 ring-emerald-300',
  degraded: 'bg-amber-100 text-amber-700 ring-amber-300',
  offline: 'bg-rose-100 text-rose-700 ring-rose-300',
  critical: 'bg-rose-100 text-rose-700 ring-rose-300',
  warning: 'bg-amber-100 text-amber-700 ring-amber-300',
  info: 'bg-sky-100 text-sky-700 ring-sky-300',
  neutral: 'bg-navy-100 text-navy-700 ring-navy-200',
  passed: 'bg-emerald-100 text-emerald-700 ring-emerald-300',
  failed: 'bg-rose-100 text-rose-700 ring-rose-300',
  high: 'bg-rose-100 text-rose-700 ring-rose-300',
  medium: 'bg-amber-100 text-amber-700 ring-amber-300',
  low: 'bg-sky-100 text-sky-700 ring-sky-300',
  approved: 'bg-emerald-100 text-emerald-700 ring-emerald-300',
  rejected: 'bg-rose-100 text-rose-700 ring-rose-300',
  pending: 'bg-amber-100 text-amber-700 ring-amber-300',
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
