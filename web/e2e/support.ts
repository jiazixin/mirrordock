/** Returns value, failing the test with a clear message when it is missing. */
export function defined<T>(value: T | null | undefined, what = 'value'): T {
  if (value === null || value === undefined) throw new Error(`expected ${what} to be present`)
  return value
}
