"""Compare Panzhan download strategies on one cloud link using an external clock.

Only non-sensitive counters cross the probe protocol. Each case gets a fresh
directory and transport. Existing account files are hashed before/after.
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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--exe", default=".tmp/download-matrix/panzhan-matrix.exe")
    parser.add_argument("--data", default="data")
    parser.add_argument("--scratch", default=".tmp/download-matrix")
    parser.add_argument("--plan", required=True, help="JSON list of named configurations")
    parser.add_argument("--output", required=True)
    parser.add_argument("--seconds", type=float, default=6)
    parser.add_argument("--warmup", type=float, default=1)
    args = parser.parse_args()
    if not 1 <= args.seconds <= 12 or not 0 <= args.warmup <= 3:
        parser.error("seconds [1,12], warmup [0,3]")
    plan = json.loads(Path(args.plan).read_text(encoding="utf-8"))
    if not isinstance(plan, list) or not 1 <= len(plan) <= 32:
        parser.error("plan must contain 1 through 32 cases")
    before = {name: digest(Path(args.data)/name) for name in ("state.json", "master.key")}
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    report = {"exe_sha256":digest(args.exe), "plan":plan, "seconds":args.seconds,
              "warmup":args.warmup, "rounds":[]}
    process = subprocess.Popen([str(Path(args.exe).resolve()), "-matrix", "-verify-account",
        "-data", str(Path(args.data).resolve()), "-scratch", str(Path(args.scratch).resolve()),
        "-max-mib", "192"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, text=True, encoding="utf-8")
    responses = queue.Queue()

    def read_output():
        for line in process.stdout:
            try:
                responses.put(json.loads(line))
            except json.JSONDecodeError:
                responses.put({"event":"error", "error":"invalid probe output"})
        responses.put({"event":"error", "error":"probe exited"})

    def discard_errors():
        # Runtime diagnostics could contain internals; do not persist or print.
        process.stderr.read()

    threading.Thread(target=read_output,daemon=True).start()
    threading.Thread(target=discard_errors,daemon=True).start()

    def receive(timeout=30):
        result = responses.get(timeout=timeout)
        if result["event"] == "error":
            raise RuntimeError(result["error"])
        return result

    def request(command):
        start = time.monotonic()
        process.stdin.write(command+"\n")
        process.stdin.flush()
        value = receive()
        end = time.monotonic()
        return (start+end)/2, value, end-start

    def save():
        output.write_text(json.dumps(report,indent=2)+"\n",encoding="utf-8")

    try:
        report["ready"] = receive(60)
        for index, config in enumerate(plan,1):
            print(f"Case {index}/{len(plan)}: {config['name']}",flush=True)
            start, _, _ = request("start "+json.dumps({k:config[k] for k in ("workers","chunk_mib","protocol")}))
            time.sleep(args.warmup)
            first_time, first, first_latency = request("sample")
            time.sleep(args.seconds)
            last_time, last, last_latency = request("sample")
            stop_time, stopped, _ = request("stop")
            elapsed = last_time-first_time
            received = last["received"]-first["received"]
            statuses = stopped["statuses"]
            valid = (not last["done"] and not stopped["error"] and bool(statuses)
                     and set(statuses) == {"206"}
                     and (config["protocol"] == "auto" or
                          last["protocol"] == {"h1":"HTTP/1.1","h2":"HTTP/2.0"}[config["protocol"]])
                     and max(first_latency,last_latency) < 0.1
                     and stopped.get("repeated_ranges",0) == 0)
            result = {"index":index, **config, "external_seconds":elapsed,
                "received_bytes":received, "mib_per_second":received/elapsed/(1<<20),
                "total_received_bytes":stopped["received"],
                "whole_run_mib_per_second":stopped["received"]/(stop_time-start)/(1<<20),
                "protocol_observed":last["protocol"], "connections":last["connections"],
                "peak_active_responses":last["peak_active_responses"],
                "active_at_window_start":first["active_responses"],
                "requests":stopped["requests"], "statuses":statuses,
                "repeated_ranges":stopped.get("repeated_ranges"),
                "error":stopped["error"], "ended_early":last["done"],
                "ipc_max_seconds":max(first_latency,last_latency), "valid_sample":valid}
            report["rounds"].append(result)
            save()
            print(json.dumps(result),flush=True)
            if not valid:
                raise RuntimeError("invalid sample; stopped rather than comparing failures")
        grouped = {}
        for result in report["rounds"]:
            grouped.setdefault(result["name"],[]).append(result["mib_per_second"])
        report["median_mib_per_second"] = {name:statistics.median(values) for name,values in grouped.items()}
        process.stdin.write("quit\n")
        process.stdin.flush()
        process.stdin.close()
        process.wait(timeout=15)
        if process.returncode:
            raise RuntimeError("probe did not exit cleanly")
    finally:
        if process.poll() is None:
            try:
                process.stdin.write("quit\n")
                process.stdin.flush()
                process.wait(timeout=15)
            except (OSError,subprocess.TimeoutExpired):
                process.kill()
                process.wait(timeout=5)
        report["account_files_unchanged"] = all(digest(Path(args.data)/name)==value for name,value in before.items())
        save()
        print("Account files unchanged:",report["account_files_unchanged"],flush=True)


if __name__ == "__main__":
    main()
