export type Poller = {
  start(): void;
  stop(): void;
  isRunning(): boolean;
};

export function createPoller(intervalMs: number, task: () => Promise<void>): Poller {
  let timer: NodeJS.Timeout | null = null;
  let stopped = true;

  function schedule(): void {
    if (stopped) return;
    timer = setTimeout(async () => {
      timer = null;
      try {
        await task();
      } finally {
        schedule();
      }
    }, intervalMs);
    timer.unref?.();
  }

  return {
    start() {
      if (!stopped) return;
      stopped = false;
      schedule();
    },
    stop() {
      stopped = true;
      if (timer) {
        clearTimeout(timer);
        timer = null;
      }
    },
    isRunning: () => !stopped
  };
}
