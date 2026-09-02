// The cast asserts on behalf of the caller. `field` is a plain string, so the returned
// value is a member of S only while the field argument is drawn from the union S is
// built over; a field that is not reports as S and is not. toggleSort.test.ts pins the
// four transitions and the prefix case, not that property.
export function toggleSort<S extends string>(field: string, current: S): S {
  const next =
    current.replace('-', '') === field
      ? current.startsWith('-')
        ? field
        : `-${field}`
      : field
  return next as S
}
