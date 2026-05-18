import { ReactNode } from "react";

export type BadgeVariant = "default" | "success" | "warning" | "danger" | "muted" | "info";

const styles: Record<BadgeVariant, string> = {
  default: "bg-slate-100 text-slate-700 border-slate-200",
  success: "bg-green-100 text-green-800 border-green-200",
  warning: "bg-amber-100 text-amber-800 border-amber-200",
  danger: "bg-red-100 text-red-800 border-red-200",
  muted: "bg-gray-100 text-gray-500 border-gray-200",
  info: "bg-blue-100 text-blue-800 border-blue-200",
};

export function Badge({
  children,
  variant = "default",
  title,
  className = "",
}: {
  children: ReactNode;
  variant?: BadgeVariant;
  title?: string;
  className?: string;
}) {
  return (
    <span
      title={title}
      className={`inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-xs font-medium whitespace-nowrap ${styles[variant]} ${className}`}
    >
      {children}
    </span>
  );
}
