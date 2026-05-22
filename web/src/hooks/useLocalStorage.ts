import { useEffect, useState } from 'react';

// useLocalStorage persists a primitive (or JSON-serializable) value under the
// given key. SSR-safe: falls back to `initial` if `localStorage` is not
// available (Vite SSR isn't in play here but the guard is cheap).
//
// Pass `parse`/`serialize` for non-string types; defaults treat the value as
// a string.
export function useLocalStorage(key: string, initial: string): [string, (v: string) => void];
export function useLocalStorage<T>(
  key: string,
  initial: T,
  parse: (raw: string) => T,
  serialize: (v: T) => string,
): [T, (v: T) => void];
export function useLocalStorage<T>(
  key: string,
  initial: T,
  parse?: (raw: string) => T,
  serialize?: (v: T) => string,
): [T, (v: T) => void] {
  const [value, setValue] = useState<T>(() => {
    if (typeof window === 'undefined') return initial;
    const raw = window.localStorage.getItem(key);
    if (raw === null) return initial;
    try {
      return parse ? parse(raw) : (raw as unknown as T);
    } catch {
      return initial;
    }
  });

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const s = serialize ? serialize(value) : (value as unknown as string);
    window.localStorage.setItem(key, s);
  }, [key, value, serialize]);

  return [value, setValue];
}
