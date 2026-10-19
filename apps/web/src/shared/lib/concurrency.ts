// A cap on how many requests of one kind run at once, for screens that must read one detail per
// row until the API offers the list with that detail. Waiting tasks start in the order they came.

export type Limiter = <T>(task: () => Promise<T>) => Promise<T>;

export function createLimiter(max: number): Limiter {
  if (!Number.isInteger(max) || max < 1) throw new Error(`Concurrency limit must be a positive integer: ${max}`);
  let running = 0;
  const waiting: (() => void)[] = [];

  const release = () => {
    running -= 1;
    waiting.shift()?.();
  };

  return async <T>(task: () => Promise<T>): Promise<T> => {
    if (running >= max) await new Promise<void>((resolve) => waiting.push(resolve));
    running += 1;
    try {
      return await task();
    } finally {
      release();
    }
  };
}
