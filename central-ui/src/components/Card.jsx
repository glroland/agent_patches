export default function Card({ title, subtitle, action, children, className = '' }) {
  return (
    <div className={`rounded-xl border border-navy-200 bg-white shadow-sm shadow-black/20 ${className}`}>
      {(title || action) && (
        <div className="flex items-start justify-between border-b border-navy-200 px-5 py-4">
          <div>
            {title && <h3 className="text-sm font-semibold text-black">{title}</h3>}
            {subtitle && <p className="mt-0.5 text-xs text-navy-500">{subtitle}</p>}
          </div>
          {action}
        </div>
      )}
      <div className="p-5">{children}</div>
    </div>
  );
}
