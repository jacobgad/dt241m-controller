export async function mapWithConcurrency<T, R>(
  items: readonly T[],
  limit: number,
  fn: (item: T) => Promise<R>
): Promise<R[]> {
  const results: R[] = new Array(items.length);
  let next = 0;

  async function worker(): Promise<void> {
    for (;;) {
      const index = next;
      next += 1;
      if (index >= items.length) return;
      results[index] = await fn(items[index]!);
    }
  }

  const workers = Math.max(1, Math.min(limit, items.length));
  await Promise.all(Array.from({ length: workers }, () => worker()));
  return results;
}

export class KeyedQueue {
  private readonly tails = new Map<string, Promise<unknown>>();
  private readonly pending = new Map<string, number>();

  enqueue<T>(key: string, task: () => Promise<T>): Promise<T> {
    const previous = this.tails.get(key) ?? Promise.resolve();
    this.pending.set(key, (this.pending.get(key) ?? 0) + 1);
    const run = previous.then(
      () => task(),
      () => task()
    );
    this.tails.set(key, run);
    void run
      .finally(() => {
        const remaining = (this.pending.get(key) ?? 1) - 1;
        if (remaining <= 0) this.pending.delete(key);
        else this.pending.set(key, remaining);
        if (this.tails.get(key) === run) this.tails.delete(key);
      })
      .catch(() => {});
    return run;
  }

  pendingCount(key: string): number {
    return this.pending.get(key) ?? 0;
  }
}
