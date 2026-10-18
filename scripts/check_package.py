"""Validate the native package, or run the build staging regression checks."""
from __future__ import annotations

import argparse
import importlib.util
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class BuildTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        (ROOT / ".cache").mkdir(exist_ok=True)

    def load_builder(self):
        location = ROOT / "scripts/build.py"
        self.assertTrue(location.is_file(), "native package builder must exist")
        spec = importlib.util.spec_from_file_location("package_build", location)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module

    def test_rejects_windows_or_wrong_architecture_binary(self):
        builder = self.load_builder()
        with tempfile.TemporaryDirectory(dir=ROOT / ".cache") as tmp:
            binary = Path(tmp) / "server"
            for data in [b"MZ" + bytes(64), b"\x7fELF\x02\x01" + bytes(64)]:
                binary.write_bytes(data)
                with self.assertRaises(ValueError):
                    builder.validate_elf(binary)

    def test_stage_contains_native_binary_license_and_desktop_entry(self):
        builder = self.load_builder()
        with tempfile.TemporaryDirectory(dir=ROOT / ".cache") as tmp:
            binary = Path(tmp) / "server"
            header = bytearray(64)
            header[:6] = b"\x7fELF\x02\x01"
            header[18:20] = (62).to_bytes(2, "little")
            binary.write_bytes(header)
            stage = Path(tmp) / "package"
            builder.stage_package(binary, stage)
            self.assertEqual((stage / "app/bin/panzhan").read_bytes(), bytes(header))
            self.assertTrue((stage / "app/LICENSE").is_file())
            self.assertTrue((stage / "app/THIRD_PARTY_NOTICES.md").is_file())
            for license_file in (ROOT / "licenses").rglob("*"):
                if license_file.is_file():
                    self.assertEqual((stage / "app/licenses" / license_file.relative_to(ROOT / "licenses")).read_bytes(), license_file.read_bytes())
            self.assertEqual(json.loads((stage / "config/privilege").read_text())["defaults"]["run-as"], "package")
            entry = json.loads((stage / "app/ui/config").read_text())[".url"]["panzhan.main"]
            self.assertEqual(entry["port"], "9867")
            self.assertEqual(entry["type"], "url")
            self.assertEqual(json.loads((stage / "config/resource").read_text())["data-share"]["shares"], [{"name": "panzhan/downloads"}])
            wizard = json.loads((stage / "wizard/install").read_text())
            fields = {item.get("field") for step in wizard for item in step["items"]}
            self.assertIn("wizard_admin_password", fields)
            self.assertIn("wizard_admin_password_confirm", fields)
            for script in (stage / "cmd").iterdir():
                self.assertNotIn(b"\r", script.read_bytes(), "staged lifecycle scripts must use LF even on Windows")

    def test_fpk_rejects_binary_without_owner_executable_permission(self):
        self.assertTrue(callable(globals().get("check_fpk")), "FPK checker must reject non-executable payloads")
        with tempfile.TemporaryDirectory(dir=ROOT / ".cache") as tmp:
            payload = io.BytesIO()
            with tarfile.open(fileobj=payload, mode="w:gz") as inner:
                item = tarfile.TarInfo("bin/panzhan")
                item.mode = 0o641
                item.size = 64
                inner.addfile(item, io.BytesIO(bytes(64)))
            data = payload.getvalue()
            manifest = f"checksum={hashlib.md5(data).hexdigest()}\n".encode()
            package = Path(tmp) / "bad.fpk"
            with tarfile.open(package, mode="w:gz") as outer:
                for name, content in [("app.tgz", data), ("manifest", manifest)]:
                    item = tarfile.TarInfo(name)
                    item.size = len(content)
                    outer.addfile(item, io.BytesIO(content))
            with self.assertRaisesRegex(ValueError, "executable"):
                check_fpk(package)


@unittest.skipUnless(sys.platform == "linux", "Linux /proc lifecycle checks require Linux or WSL")
class LifecycleTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="panzhan-lifecycle-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "app/bin").mkdir(parents=True)
        (self.root / "data").mkdir()
        shutil.copyfile("/bin/sleep", self.root / "app/bin/panzhan")
        (self.root / "app/bin/panzhan").chmod(0o755)
        self.env = os.environ.copy()
        self.env.update(TRIM_APPDEST=str(self.root / "app"), TRIM_PKGVAR=str(self.root / "data"))
        self.env.pop("TRIM_DATA_SHARE_PATHS", None)

    def command(self, action):
        return subprocess.run(["bash", str(ROOT / "packaging/fnos/cmd/main"), action], env=self.env, capture_output=True, text=True, timeout=35)

    def record_process(self, binary):
        process = subprocess.Popen([str(binary), "30"])
        def cleanup():
            if process.poll() is None:
                process.kill()
            process.wait()
        self.addCleanup(cleanup)
        start = Path(f"/proc/{process.pid}/stat").read_text().rsplit(") ", 1)[1].split()[19]
        (self.root / "data/panzhan.pid").write_text(f"{process.pid} {start}\n")
        return process

    def test_missing_share_has_actionable_error(self):
        result = self.command("start")
        self.assertEqual(result.returncode, 1)
        self.assertIn("shared directory is unavailable", result.stderr)

    def test_stop_does_not_signal_an_unrelated_process(self):
        process = self.record_process("/bin/sleep")
        self.assertEqual(self.command("status").returncode, 3)
        self.assertEqual(self.command("stop").returncode, 0)
        self.assertIsNone(process.poll())

    def test_status_and_stop_recognize_owned_process(self):
        process = self.record_process(self.root / "app/bin/panzhan")
        self.assertEqual(self.command("status").returncode, 0)
        self.assertEqual(self.command("stop").returncode, 0)
        self.assertEqual(process.wait(timeout=2), -15)


def check_fpk(package: Path):
    with tarfile.open(package) as outer:
        payload = outer.extractfile("app.tgz").read()
        manifest = outer.extractfile("manifest").read().decode("utf8")
        fields = dict(line.split("=", 1) for line in manifest.splitlines() if "=" in line)
        fields = {key.strip(): value.strip() for key, value in fields.items()}
        if fields.get("checksum") != hashlib.md5(payload).hexdigest():
            raise ValueError("official fnpack application checksum does not match payload")
        with tarfile.open(fileobj=io.BytesIO(payload)) as inner:
            binary = inner.getmember("bin/panzhan")
            if not binary.mode & 0o100:
                raise ValueError("native binary must be executable; build on Linux or use --wsl with Linux fnpack")
            header = inner.extractfile(binary).read(64)
            if len(header) < 64 or header[:6] != b"\x7fELF\x02\x01" or int.from_bytes(header[18:20], "little") != 62:
                raise ValueError("FPK payload is not a Linux x86_64 ELF")
            inner.getmember("LICENSE")
            inner.getmember("THIRD_PARTY_NOTICES.md")
            if not any(item.name.startswith("licenses/") and item.isfile() for item in inner.getmembers()):
                raise ValueError("FPK must include third-party license texts")
            for name in ["ui/config", "config/privilege", "config/resource"]:
                json.loads(inner.extractfile(name).read())
        for name in ["cmd/main", "cmd/install_callback", "config/privilege", "config/resource", "ICON.PNG", "ICON_256.PNG", "wizard/install"]:
            outer.getmember(name)
        wizard = json.loads(outer.extractfile("wizard/install").read())
        wizard_fields = {item.get("field") for step in wizard for item in step["items"]}
        if not {"wizard_admin_password", "wizard_admin_password_confirm"}.issubset(wizard_fields):
            raise ValueError("FPK install wizard must collect and confirm the initial password")
        privilege = json.loads(outer.extractfile("config/privilege").read())
        if privilege.get("defaults", {}).get("run-as") != "package":
            raise ValueError("native application must run as the dedicated package user")
        resource = json.loads(outer.extractfile("config/resource").read())
        if resource.get("data-share", {}).get("shares") != [{"name": "panzhan/downloads"}]:
            raise ValueError("native application must declare only its dedicated download share")
        for item in outer.getmembers():
            if item.name.startswith("cmd/") and item.isfile() and not item.mode & 0o100:
                raise ValueError(f"lifecycle script must be executable: {item.name}")
        if fields.get("platform") != "x86" or fields.get("appname") != "panzhan":
            raise ValueError("FPK manifest appname/platform mismatch")
    print(f"FPK checksum, native payload and executable permissions verified: {package}")


def check_stage(stage: Path):
    spec = importlib.util.spec_from_file_location("package_build", ROOT / "scripts/build.py")
    builder = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(builder)
    builder.validate_elf(stage / "app/bin/panzhan")
    for name in ["manifest", "ICON.PNG", "ICON_256.PNG", "cmd/main", "app/LICENSE", "app/THIRD_PARTY_NOTICES.md", "wizard/install"]:
        if not (stage / name).is_file():
            raise ValueError(f"missing package file: {name}")
    for name in ["config/privilege", "config/resource", "app/ui/config", "wizard/install"]:
        json.loads((stage / name).read_text(encoding="utf8"))
    for script in (stage / "cmd").iterdir():
        if b"\r" in script.read_bytes():
            raise ValueError(f"lifecycle script must use LF line endings: {script.name}")
    print(f"Native x86_64 package structure verified: {stage}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--stage", type=Path)
    parser.add_argument("--fpk", type=Path)
    args = parser.parse_args()
    if args.self_test:
        unittest.main(argv=[__file__])
    elif args.stage:
        check_stage(args.stage)
    elif args.fpk:
        check_fpk(args.fpk)
    else:
        parser.error("choose --self-test, --stage PATH or --fpk PATH")
