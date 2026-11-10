package io.pressurecooker.jmeter;

import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.channels.FileChannel;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.nio.file.StandardOpenOption;
import java.util.Arrays;
import java.util.Map;
import java.util.Properties;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import org.apache.jmeter.engine.util.NoConfigMerge;
import org.apache.jmeter.engine.util.NoThreadClone;
import org.apache.jmeter.testelement.AbstractTestElement;
import org.apache.jmeter.testelement.TestStateListener;
import org.apache.jmeter.threads.JMeterContextService;

/** Node-local lifecycle observer; creates no JMeter threads or sample results. */
public final class ThreadObservationListener extends AbstractTestElement
        implements TestStateListener, NoThreadClone, NoConfigMerge {
    private static final long serialVersionUID = 1L;
    private static final long MAX_BYTES = 4L << 20;
    private static final int MAX_POINTS = 90000;
    private static final long INTERVAL_MS = 1000;
    private transient Capture capture;
    private transient boolean attempted;

    @Override
    public synchronized void testStarted() {
        if (attempted) {
            return;
        }
        attempted = true;
        try {
            capture = openCapture();
            if (capture != null) {
                capture.start();
            }
        } catch (IOException | RuntimeException ignored) {
            // Collection failure must not change business execution. The
            // executor will report MISSING/INVALID rather than invent readings.
            if (capture != null) {
                capture.abort();
            }
        }
    }

    @Override
    public void testStarted(String host) {
        testStarted();
    }

    @Override
    public synchronized void testEnded() {
        if (capture != null) {
            capture.finish();
        }
    }

    @Override
    public void testEnded(String host) {
        testEnded();
    }

    private Capture openCapture() throws IOException {
        String task = getPropertyAsString("task_id");
        String attempt = getPropertyAsString("attempt_id");
        String owner = getPropertyAsString("owner_key");
        String rootValue = System.getProperty("pressurecooker.capture.tasks_root", "");
        if (!validID(task) || !validID(attempt) || !owner.matches("[0-9a-f]{64}") || rootValue.isEmpty()) {
            return null;
        }
        Path root = Paths.get(rootValue).toAbsolutePath().normalize();
        Path dir = root.resolve(task).resolve("thread-observations").resolve(attempt);
        Path binding = dir.resolve("binding.properties");
        if (!safePath(dir) || !Files.isRegularFile(binding, LinkOption.NOFOLLOW_LINKS)
                || Files.size(binding) > 4096) {
            return null;
        }
        byte[] bindingBytes = Files.readAllBytes(binding);
        if (bindingBytes.length > 4096) {
            return null;
        }
        Properties b = new Properties();
        b.load(new ByteArrayInputStream(bindingBytes));
        String node = b.getProperty("node_id", "");
        String generation = b.getProperty("generation", "");
        String name = b.getProperty("file", "");
        if (!task.equals(b.getProperty("task_id")) || !attempt.equals(b.getProperty("attempt_id"))
                || !owner.equals(b.getProperty("owner_key")) || !validID(node)
                || !generation.matches("[1-9][0-9]*") || Long.parseLong(generation) < 1
                || !generation.equals(System.getProperty("pressurecooker.capture.generation", ""))
                || !node.equals(System.getProperty("pressurecooker.capture.node_id", ""))
                || !name.equals(owner + "-" + generation + ".csv")) {
            return null;
        }
        long device = Long.parseLong(b.getProperty("device", ""));
        long inode = Long.parseLong(b.getProperty("inode", ""));
        Path path = dir.resolve(name);
        byte[] header = ("pressurecooker_thread_observations_v1," + task + "," + attempt + ","
                + node + "," + generation + "," + INTERVAL_MS + "\ntimestamp_ms,active_threads\n")
                .getBytes(StandardCharsets.US_ASCII);
        // A listener can start only against the exact freshly prepared inode.
        // Replaying a test or a stale owner cannot append to completed output.
        if (!sameFile(path, device, inode) || Files.size(path) != header.length
                || !Arrays.equals(header, Files.readAllBytes(path))) {
            return null;
        }
        FileChannel channel = FileChannel.open(path, StandardOpenOption.WRITE,
                StandardOpenOption.APPEND, LinkOption.NOFOLLOW_LINKS);
        if (!sameFile(path, device, inode) || channel.size() != header.length) {
            channel.close();
            return null;
        }
        return new Capture(path, channel, device, inode, header.length);
    }

    private static boolean validID(String value) {
        return value.matches("[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}");
    }

    private static boolean safePath(Path path) throws IOException {
        return path.isAbsolute() && path.equals(path.toRealPath());
    }

    private static boolean sameFile(Path path, long device, long inode) throws IOException {
        if (!safePath(path.getParent()) || !Files.isRegularFile(path, LinkOption.NOFOLLOW_LINKS)) {
            return false;
        }
        Map<String, Object> attrs = Files.readAttributes(path, "unix:dev,ino,nlink", LinkOption.NOFOLLOW_LINKS);
        return ((Number) attrs.get("dev")).longValue() == device
                && ((Number) attrs.get("ino")).longValue() == inode
                && ((Number) attrs.get("nlink")).longValue() == 1;
    }

    private static final class Capture {
        private final Path path;
        private final FileChannel channel;
        private final long device;
        private final long inode;
        private final ScheduledExecutorService scheduler;
        private long bytes;
        private long lastTimestamp = -1;
        private int points;
        private boolean stopped;

        Capture(Path path, FileChannel channel, long device, long inode, long bytes) {
            this.path = path;
            this.channel = channel;
            this.device = device;
            this.inode = inode;
            this.bytes = bytes;
            this.scheduler = Executors.newSingleThreadScheduledExecutor(action -> {
                Thread thread = new Thread(action, "pressurecooker-thread-observer");
                thread.setDaemon(true);
                return thread;
            });
        }

        synchronized void start() {
            sample();
            if (!stopped) {
                scheduler.scheduleAtFixedRate(this::sample, INTERVAL_MS, INTERVAL_MS, TimeUnit.MILLISECONDS);
            }
        }

        synchronized void sample() {
            if (stopped) {
                return;
            }
            try {
                long stamp = System.currentTimeMillis();
                if (stamp <= lastTimestamp) {
                    return;
                }
                // This counter is maintained by actual JMeter worker threads.
                int active = JMeterContextService.getNumberOfThreads();
                if (points >= MAX_POINTS || active < 0 || active > 1000000000
                        || !append(stamp + "," + active + "\n")) {
                    abort();
                    return;
                }
                points++;
                lastTimestamp = stamp;
            } catch (IOException | RuntimeException ignored) {
                abort();
            }
        }

        private boolean append(String value) throws IOException {
            byte[] data = value.getBytes(StandardCharsets.US_ASCII);
            // Reserve room for END, but never write it when an error/limit kills
            // collection. A forced process kill has no shutdown hook here.
            if (bytes + data.length + 32 > MAX_BYTES || !sameFile(path, device, inode)
                    || channel.size() != bytes) {
                return false;
            }
            ByteBuffer buffer = ByteBuffer.wrap(data);
            while (buffer.hasRemaining()) {
                channel.write(buffer);
            }
            channel.force(false);
            bytes += data.length;
            return true;
        }

        synchronized void finish() {
            if (stopped) {
                return;
            }
            scheduler.shutdownNow();
            sample(); // A normal lifecycle end gets an actual final reading.
            if (!stopped) {
                try {
                    append("END," + Math.max(System.currentTimeMillis(), lastTimestamp) + "\n");
                } catch (IOException | RuntimeException ignored) {
                    // Keep the valid interrupted prefix; never synthesize zero.
                }
                abort();
            }
        }

        synchronized void abort() {
            stopped = true;
            scheduler.shutdownNow();
            try {
                channel.close();
            } catch (IOException ignored) {
                // The supervisor validates and archives whatever is durable.
            }
        }
    }
}
