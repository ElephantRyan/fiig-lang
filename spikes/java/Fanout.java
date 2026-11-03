import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Semaphore;
import java.util.concurrent.atomic.AtomicInteger;

public class Fanout {
    static final int TOTAL_ITEMS = 10_000;
    static final int CONCURRENCY = 50;
    static final long FAKE_LATENCY_MS = 5;

    public static void main(String[] args) throws InterruptedException {
        Semaphore sem = new Semaphore(CONCURRENCY);
        AtomicInteger active = new AtomicInteger(0);
        AtomicInteger peak = new AtomicInteger(0);
        CountDownLatch latch = new CountDownLatch(TOTAL_ITEMS);

        long start = System.nanoTime();

        try (ExecutorService executor = Executors.newVirtualThreadPerTaskExecutor()) {
            for (int i = 0; i < TOTAL_ITEMS; i++) {
                sem.acquire();
                executor.submit(() -> {
                    try {
                        int n = active.incrementAndGet();
                        peak.updateAndGet(p -> Math.max(p, n));
                        try {
                            Thread.sleep(FAKE_LATENCY_MS);
                        } catch (InterruptedException ignored) {
                        }
                        active.decrementAndGet();
                    } finally {
                        sem.release();
                        latch.countDown();
                    }
                });
            }
            latch.await();
        }

        long elapsedMs = (System.nanoTime() - start) / 1_000_000;
        Runtime rt = Runtime.getRuntime();
        double heapMb = (rt.totalMemory() - rt.freeMemory()) / 1024.0 / 1024.0;

        System.out.printf("items=%d concurrency=%d elapsed=%dms peak_concurrent=%d heap_used_mb=%.2f%n",
                TOTAL_ITEMS, CONCURRENCY, elapsedMs, peak.get(), heapMb);
    }
}
