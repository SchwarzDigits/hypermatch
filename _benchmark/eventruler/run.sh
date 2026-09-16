#!/bin/sh
# Downloads AWS Event Ruler and its dependencies, compiles the benchmark and
# runs it with and without the wildcard condition. Needs a JDK and curl; the
# downloaded jars and the compiled classes stay in this folder.
set -e

dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
lib="$dir/lib"
mkdir -p "$lib" "$dir/classes"

base=https://repo1.maven.org/maven2
fetch() {
	file="$lib/${1##*/}"
	if [ ! -f "$file" ]; then
		echo "downloading ${1##*/}" >&2
		curl -fsSL -o "$file" "$1"
	fi
}

fetch $base/software/amazon/event/ruler/event-ruler/2.2.0/event-ruler-2.2.0.jar
fetch $base/com/fasterxml/jackson/core/jackson-databind/2.22.2/jackson-databind-2.22.2.jar
fetch $base/com/fasterxml/jackson/core/jackson-core/2.22.2/jackson-core-2.22.2.jar
fetch $base/com/fasterxml/jackson/core/jackson-annotations/2.22/jackson-annotations-2.22.jar
fetch $base/ch/randelshofer/fastdoubleparser/2.0.1/fastdoubleparser-2.0.1.jar
fetch $base/com/google/code/findbugs/jsr305/3.0.2/jsr305-3.0.2.jar

javac -cp "$lib/*" -d "$dir/classes" "$dir/EventRulerBenchmark.java"

# The JVM runs with its default settings, like the Go suite does.
for wildcard in true false; do
	java -cp "$lib/*:$dir/classes" EventRulerBenchmark -wildcard=$wildcard
done
