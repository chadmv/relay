// The base name of a sort value: 'name' and '-name' both reduce to 'name'. Constraining
// `field` to this is what makes a typo'd column a compile error at the call site rather
// than an S-typed value that is not a member of S. S still infers from `current` alone.
type SortFieldOf<S extends string> = S extends `-${infer F}` ? F : S

// The cast asserts on behalf of the caller: the template literal widens to string, and
// only the constraint above keeps the result inside S.
export function toggleSort<S extends string>(field: SortFieldOf<S>, current: S): S {
  const next = current === field ? `-${field}` : field
  return next as S
}
