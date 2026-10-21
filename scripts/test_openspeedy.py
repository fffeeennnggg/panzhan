"""Measure the production download engine with an optional OpenSpeedy DLL.

The controller's monotonic clock is in a separate, unpatched process. Account
credentials and signed cloud URLs never cross the probe's stdout protocol.
"""
import argparse
import hashlib
import json
from pathlib import Path
import queue
import statistics
import subprocess
import threading
import time


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def one_run(args, factor, patched):
    command = [str(Path(args.exe).resolve()), "-data", str(Path(args.data).resolve()),
               "-scratch", str(Path(args.scratch).resolve()), "-factor", str(factor)]
    if patched:
        command += ["-speedpatch", str(Path(args.dll).resolve())]
    if args.clock_only:
        command += ["-clock-only"]
    if args.verify_account:
        command += ["-verify-account"]
    if args.switch_in_place:
        command += ["-max-mib", "512"]
    process = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, text=True, encoding="utf-8")
    responses = queue.Queue()
    errors = []

    def read_responses():
        for line in process.stdout:
            try:
                responses.put(json.loads(line))
            except json.JSONDecodeError:
                responses.put({"event": "error", "error": "non-JSON probe output"})
        responses.put({"event": "error", "error": "probe process exited"})

    def read_errors():
        # Keep diagnostics in memory; do not print potential runtime internals.
        errors.append(process.stderr.read())

    threading.Thread(target=read_responses, daemon=True).start()
    threading.Thread(target=read_errors, daemon=True).start()

    def receive(timeout=15):
        message = responses.get(timeout=timeout)
        if message["event"] == "error":
            raise RuntimeError(message["error"])
        return message

    def request(command):
        before = time.monotonic()
        process.stdin.write(command + "\n")
        process.stdin.flush()
        message = receive()
        return (before + time.monotonic()) / 2, message

    try:
        ready = receive(45)
        request("start")
        results = []
        for phase, current_factor in enumerate(args.factors if args.switch_in_place else [factor], 1):
            if args.switch_in_place:
                request(f"speed {current_factor}")
            time.sleep(args.warmup)
            measured_start, first = request("sample")
            deadline = measured_start + args.seconds
            while time.monotonic() < deadline:
                time.sleep(min(0.5, max(0, deadline-time.monotonic())))
            measured_end, last = request("sample")
            elapsed = measured_end - measured_start
            result = {**ready, "event":"result", "factor":current_factor,
                      "phase":phase, "switch_in_place":args.switch_in_place,
                      "external_seconds":elapsed, "warmup_seconds":args.warmup,
                      "received_bytes":last["received"]-first["received"],
                      "mib_per_second":(last["received"]-first["received"])/elapsed/(1<<20),
                      "win_tick_rate":(last["win_tick_ms"]-first["win_tick_ms"])/1000/elapsed,
                      "win_filetime_rate":(last["win_filetime_100ns"]-first["win_filetime_100ns"])/1e7/elapsed,
                      "go_clock_rate":(last["go_elapsed_ns"]-first["go_elapsed_ns"])/1e9/elapsed,
                      "http_status":last["http_status"], "protocol":last["protocol"],
                      "response_info":last["response_info"], "requests":last["requests"],
                      "ended_early":last["done"], "error":last["error"]}
            result["clock_matches_factor"] = all(abs(result[key]/current_factor - 1) < 0.15
                                                  for key in ("win_tick_rate", "win_filetime_rate"))
            result["valid_sample"] = result["clock_matches_factor"] and (args.clock_only or
                (not last["done"] and not last["error"] and last["http_status"] == 206))
            results.append(result)
            if not result["valid_sample"]:
                break
        _, stopped = request("stop")
        process.stdin.close()
        process.wait(timeout=15)
        if process.returncode != 0:
            raise RuntimeError(f"probe exited with code {process.returncode}")
        for result in results:
            result["exit_code"] = process.returncode
            result["total_received_bytes"] = stopped["received"]
            if stopped["error"]:
                result["error"] = stopped["error"]
                result["valid_sample"] = False
        return results
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(timeout=5)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--exe", default=".tmp/openspeedy-probe/panzhan-speedprobe.exe")
    parser.add_argument("--dll", default=".tmp/openspeedy-probe/speedpatch64.dll")
    parser.add_argument("--data", default="data")
    parser.add_argument("--scratch", default=".tmp/openspeedy-probe")
    parser.add_argument("--output", default=".tmp/openspeedy-probe/results.json")
    parser.add_argument("--seconds", type=float, default=5)
    parser.add_argument("--warmup", type=float, default=1)
    parser.add_argument("--pairs", type=int, default=3)
    parser.add_argument("--factors", type=float, nargs="+", default=[1, 5])
    parser.add_argument("--switch-in-place", action="store_true",
                        help="Change factors during one continuous download per repetition")
    parser.add_argument("--clock-only", action="store_true")
    parser.add_argument("--verify-account", action="store_true")
    args = parser.parse_args()
    if not 0 < args.seconds <= 30 or not 0 <= args.warmup <= 5 or not 1 <= args.pairs <= 5:
        parser.error("seconds must be (0,30], warmup [0,5], pairs [1,5]")
    if not 1 <= len(args.factors) <= 6 or any(not 1 <= f <= 100 for f in args.factors):
        parser.error("provide 1 through 6 factors, each between 1 and 100")
    baseline = {name: digest(Path(args.data)/name) for name in ("state.json", "master.key")}
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    report = {"clock_only": args.clock_only, "rounds": [],
              "exe_sha256":digest(args.exe), "dll_sha256":digest(args.dll)}
    sequence = ([(args.factors[0], True)] * args.pairs if args.switch_in_place else
                [(1, False)] + [(factor, True) for _ in range(args.pairs) for factor in args.factors])
    try:
        for index, (factor, patched) in enumerate(sequence, 1):
            print(f"Round {index}/{len(sequence)}: {'OpenSpeedy' if patched else 'unpatched'} {factor}x", flush=True)
            results = one_run(args, factor, patched)
            report["rounds"].extend(results)
            output.write_text(json.dumps(report, indent=2), encoding="utf-8")
            for result in results:
                print(json.dumps(result), flush=True)
            if any(not result["valid_sample"] for result in results):
                raise RuntimeError("invalid sample; stopping instead of comparing failed runs")
        rates = {}
        for factor in dict.fromkeys(args.factors):
            samples = [r["mib_per_second"] for r in report["rounds"] if r["patched"] and r["factor"] == factor]
            rates[str(factor)] = statistics.median(samples)
        report["median_mib_per_second"] = rates
    finally:
        report["account_files_unchanged"] = all(digest(Path(args.data)/name) == value for name, value in baseline.items())
        output.write_text(json.dumps(report, indent=2), encoding="utf-8")
        print("Account files unchanged:", report["account_files_unchanged"], flush=True)


if __name__ == "__main__":
    main()
