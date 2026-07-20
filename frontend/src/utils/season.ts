// Seasonal helpers (no external deps).

// Novembro Azul: true only during November (month index 10).
export function isNovember(date: Date): boolean {
  return date.getMonth() === 10;
}
