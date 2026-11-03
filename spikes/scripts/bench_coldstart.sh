#!/usr/bin/env bash
# usage: bench_coldstart.sh <command...>
# example: ./bench_coldstart.sh ./go/cli/cli.exe
# example: ./bench_coldstart.sh java -cp java Cli
set -euo pipefail

RUNS=100
times=()
total=0

for _ in $(seq 1 "$RUNS"); do
  start=$(date +%s%N)
  "$@" > /dev/null
  end=$(date +%s%N)
  ms=$(( (end - start) / 1000000 ))
  times+=("$ms")
  total=$((total + ms))
done

IFS=$'\n' sorted=($(sort -n <<<"${times[*]}")); unset IFS
count=${#sorted[@]}
p50=${sorted[$((count/2))]}
p99=${sorted[$((count*99/100))]}
avg=$((total / count))

echo "cmd=\"$*\" runs=$count avg_ms=$avg p50_ms=$p50 p99_ms=$p99 min_ms=${sorted[0]} max_ms=${sorted[$((count-1))]}"
