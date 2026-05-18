import { ReactNode } from "react";

export function EmptyState({
  title,
  description,
  action,
  icon,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
  icon?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center text-center py-10 px-4 text-slate-500">
      <div className="mb-2 text-slate-400">
        {icon ?? (
          <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
            <path d="M3 7h18M3 12h18M3 17h18" strokeLinecap="round" />
          </svg>
        )}
      </div>
      <div className="font-medium text-slate-700">{title}</div>
      {description && <div className="text-sm mt-1 max-w-md">{description}</div>}
      {action && <div className="mt-3">{action}</div>}
    </div>
  );
}
