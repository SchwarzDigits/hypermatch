// EventRulerBenchmark measures AWS Event Ruler with the same rules and the
// same events as the Go candidates in the parent folder, so that the numbers
// can be compared. Run it with run.sh.
//
// Like quamina, Event Ruler has no allOf, so its "tags" condition matches an
// event that contains either tag. The events are built inside the measured
// loop, as they are in the Go suite.

import java.util.List;
import java.util.Locale;
import software.amazon.event.ruler.Machine;

public final class EventRulerBenchmark {
    private static final long WARMUP_NANOS = 5_000_000_000L;
    private static final long MEASURE_NANOS = 5_000_000_000L;

    private static int rules = 100_000;
    private static int modulo = rules / 10; // ten rules share each number

    public static void main(String[] args) throws Exception {
        boolean wildcard = true;
        for (String arg : args) {
            if (arg.equals("-wildcard=false") || arg.equals("--wildcard=false")) {
                wildcard = false;
            } else if (arg.startsWith("-rules=") || arg.startsWith("--rules=")) {
                rules = Integer.parseInt(arg.substring(arg.indexOf('=') + 1));
                modulo = Math.max(rules / 10, 1);
            }
        }

        long heapBefore = usedHeap();
        Machine machine = new Machine();
        long start = System.nanoTime();
        for (int i = 0; i < rules; i++) {
            machine.addRule(Integer.toString(i), rule(i % modulo, wildcard));
            if ((i + 1) % 1000 == 0) {
                System.err.printf("added %d rules after %.1fs%n", i + 1, (System.nanoTime() - start) / 1e9);
            }
        }
        double adding = (System.nanoTime() - start) / 1e9;
        System.err.printf("adding %d rules took %.5fs%n", rules, adding);

        // The heap after building, before matching leaves garbage behind.
        long perRule = (usedHeap() - heapBefore) / rules;
        System.err.printf("heap of the rules: %.1f MB, %d B/rule%n", perRule * (double) rules / 1e6, perRule);

        long[] warm = run(machine, WARMUP_NANOS); // let the JIT compile everything
        System.err.printf("warmup: %d events, %d matches%n", warm[0], warm[1]);
        long[] result = run(machine, MEASURE_NANOS);
        long events = result[0], matches = result[1], elapsed = result[2];

        String scenario = wildcard ? "with wildcard" : "without wildcard";
        System.out.printf(Locale.US, "| %s | event-ruler | %,d | %.2f s | %,d | %.0f | %,d |%n",
                scenario, rules, adding, Math.round(events / (elapsed / 1e9)), (double) matches / events, perRule);
    }

    // run matches events for the given time and returns the number of events,
    // the number of matches and the time it took.
    private static long[] run(Machine machine, long nanos) throws Exception {
        long events = 0, matches = 0, elapsed;
        long start = System.nanoTime();
        while ((elapsed = System.nanoTime() - start) < nanos) {
            List<String> found = machine.rulesForJSONEvent(event((int) events));
            matches += found.size();
            events++;
        }
        return new long[] {events, matches, elapsed};
    }

    private static String rule(int number, boolean wildcard) {
        String name = wildcard ? "\"name\": [{\"wildcard\": \"*-myapp-*\"}]," : "";
        return "{"
                + name
                + "\"env\": [\"prod\"],"
                + "\"number\": [\"" + number + "\"],"
                + "\"tags\": [\"tag1\", \"tag2\"],"
                + "\"region\": [{\"anything-but\": [\"moon\"]}],"
                + "\"type\": [\"app\", \"database\"]"
                + "}";
    }

    private static String event(int number) {
        return "{"
                + "\"name\": \"app-myapp-" + number + "\","
                + "\"env\": \"prod\","
                + "\"number\": \"" + (number % modulo) + "\","
                + "\"tags\": [\"tag1\", \"tag2\"],"
                + "\"region\": \"earth\","
                + "\"type\": \"app\""
                + "}";
    }

    // usedHeap returns the live heap after collecting garbage, which is only
    // an estimate: the JVM decides when it really frees memory.
    private static long usedHeap() {
        Runtime runtime = Runtime.getRuntime();
        for (int i = 0; i < 4; i++) {
            System.gc();
        }
        return runtime.totalMemory() - runtime.freeMemory();
    }
}
