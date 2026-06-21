// Returns the 1-based display range for the current page.
// startOffset is the total number of rows accumulated before this page.
// pageSize is the number of rows on this page.
//
// Empty page -> { x: 0, y: 0 } (caller renders as "0 of total").
export function computePageRange(
  startOffset: number,
  pageSize: number,
): { x: number; y: number } {
  if (pageSize === 0) return { x: 0, y: 0 }
  return { x: startOffset + 1, y: startOffset + pageSize }
}
