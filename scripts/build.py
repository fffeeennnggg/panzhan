"""Cross-compile PanZhan and package it with the official fnOS fnpack tool."""
from __future__ import annotations

import argparse
import os
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def validate_elf(binary: Path):
    with binary.open("rb") as stream:
        header = stream.read(64)
    if len(header) < 64 or header[:6] != b"\x7fELF\x02\x01" or int.from_bytes(header[18:20], "little") != 62:
        raise ValueError("package requires a Linux x86_64 little-endian ELF binary")


def stage_package(binary: Path, destination: Path):
    validate_elf(binary)
    shutil.copytree(ROOT / "packaging/fnos", destination)
    target = destination / "app/bin/panzhan"
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(binary, target)
    target.chmod(0o755)
    shutil.copyfile(ROOT / "LICENSE", destination / "app/LICENSE")
    shutil.copyfile(ROOT / "THIRD_PARTY_NOTICES.md", destination / "app/THIRD_PARTY_NOTICES.md")
    shutil.copytree(ROOT / "licenses", destination / "app/licenses")
    for script in (destination / "cmd").iterdir():
        # Windows checkouts may use CRLF, which breaks Linux shebangs.
        script.write_bytes(script.read_bytes().replace(b"\r\n", b"\n"))
        script.chmod(0o755)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fnpack", type=Path, help="official fnpack executable")
    parser.add_argument("--stage-only", action="store_true", help="cross-compile and stage without invoking fnpack")
    parser.add_argument("--wsl", action="store_true", help="run Linux fnpack in an existing WSL distribution (Windows only)")
    parser.add_argument("--wsl-distribution", default="Ubuntu", help="existing WSL distribution to use (default: Ubuntu)")
    parser.add_argument("--wsl-temp-dir", default="/tmp", help="Linux temporary directory with enough space for fnpack (for example /dev/shm)")
    parser.add_argument("--output-dir", type=Path, help="build output directory inside the project (defaults to dist)")
    args = parser.parse_args()
    if os.name == "nt" and not args.stage_only and not args.wsl:
        parser.error("Windows fnpack loses Unix executable modes; use --wsl --fnpack PATH_TO_LINUX_FNPACK")
    requested_output = args.output_dir or Path("dist")
    output = requested_output if requested_output.is_absolute() else ROOT / requested_output
    output = output.resolve()
    if not output.is_relative_to(ROOT):
        raise ValueError("build output must remain inside the project")
    output.mkdir(exist_ok=True)
    # Generated from checked-in templates; never include environment or runtime data.
    subprocess.run([os.sys.executable, "-X", "utf8", str(ROOT / "scripts/build_agent_kit.py")], cwd=ROOT, check=True)
    binary = output / "panzhan-linux-amd64"
    env = os.environ.copy()
    env.update(GOOS="linux", GOARCH="amd64", CGO_ENABLED="0")
    subprocess.run(["go", "build", "-trimpath", "-o", str(binary), "./cmd/panzhan"], cwd=ROOT, env=env, check=True)
    stage = output / "fnos/panzhan"
    if not stage.resolve().is_relative_to(output.resolve()):
        raise ValueError("refusing to replace a staging directory outside dist")
    if stage.exists():
        shutil.rmtree(stage)
    stage.parent.mkdir(parents=True, exist_ok=True)
    stage_package(binary, stage)
    subprocess.run([os.sys.executable, "-X", "utf8", str(ROOT / "scripts/check_package.py"), "--stage", str(stage)], check=True)
    if args.stage_only:
        print(f"Staged: {stage}")
        return
    executable = args.fnpack
    if executable is None:
        cached = ROOT / ".cache/tools" / ("fnpack-linux-amd64" if args.wsl else "fnpack")
        executable = cached if cached.is_file() else shutil.which("fnpack")
    if not executable:
        parser.error("official fnpack is required; pass --fnpack PATH (see docs/fnos-install.md)")
    executable = Path(executable).resolve()
    if args.wsl:
        if os.name != "nt":
            parser.error("--wsl is only needed on Windows")
        paths = []
        for path in [stage, executable, output]:
            # WSL emits its own Windows-side diagnostics as UTF-16, while
            # wslpath writes UTF-8. Decode only the Linux path stream here.
            translated = subprocess.run(["wsl", "--distribution", args.wsl_distribution, "--exec", "wslpath", "-a", str(path)], check=True, capture_output=True)
            paths.append(translated.stdout.decode("utf8").strip())
        # Copy onto a Linux filesystem so tar receives real Unix mode bits.
        script = """set -eu
temp_root=${4%/}
[ -n "$temp_root" ] && [ -d "$temp_root" ]
work=$(mktemp -d "$temp_root/panzhan-fpk.XXXXXX")
cleanup() { case "$work" in "$temp_root"/panzhan-fpk.*) rm -rf -- "$work" ;; esac; }
trap cleanup EXIT
export TMPDIR="$work"
mkdir "$work/package"
cp -R -- "$1/." "$work/package/"
find "$work/package" -type d -exec chmod 755 {} +
find "$work/package" -type f -exec chmod 644 {} +
chmod 755 "$work/package/app/bin/panzhan" "$work/package/cmd/"*
cp -- "$2" "$work/fnpack"
chmod 755 "$work/fnpack"
cd "$work"
./fnpack build --directory "$work/package"
cp -- panzhan.fpk "$3/panzhan.fpk"
"""
        packed = subprocess.run(["wsl", "--distribution", args.wsl_distribution, "--exec", "sh", "-s", "--", *paths, args.wsl_temp_dir], input=script.encode("utf8"), capture_output=True)
        print(packed.stdout.decode("utf8", errors="replace"), end="")
        if packed.stderr:
            diagnostic_encoding = "utf-16-le" if b"\x00" in packed.stderr else "utf8"
            print(packed.stderr.decode(diagnostic_encoding, errors="replace"), end="", file=os.sys.stderr)
        packed.check_returncode()
    else:
        subprocess.run([str(executable), "build", "--directory", str(stage)], cwd=output, check=True)
    package = output / "panzhan.fpk"
    subprocess.run([os.sys.executable, "-X", "utf8", str(ROOT / "scripts/check_package.py"), "--fpk", str(package)], check=True)
    print(f"Official fnpack build completed: {package}")


if __name__ == "__main__":
    main()
