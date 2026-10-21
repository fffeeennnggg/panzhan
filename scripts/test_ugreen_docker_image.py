import hashlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
BUILDER = ROOT / "scripts/build_ugreen_docker_image.py"
tempfile.tempdir = str(ROOT / ".tmp")


def write_elf(path: Path, machine: int):
    header = bytearray(64)
    header[:6] = bytes.fromhex("7f454c460201")
    header[18:20] = machine.to_bytes(2, "little")
    path.write_bytes(header)


class UgreenDockerImageTests(unittest.TestCase):
    def test_builds_importable_arm64_docker_archive(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            binary = root / "panzhan"
            ca_bundle = root / "ca-certificates.crt"
            timezone = root / "Shanghai"
            output = root / "panzhan.tar"
            write_elf(binary, 183)
            ca_bundle.write_text("test-ca-bundle", encoding="ascii")
            timezone.write_bytes(b"test-zoneinfo")

            result = subprocess.run(
                [sys.executable, str(BUILDER), "--binary", str(binary),
                 "--ca-bundle", str(ca_bundle), "--timezone", str(timezone),
                 "--output", str(output), "--tag", "panzhan:test-arm64"],
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)

            with tarfile.open(output) as image:
                manifest = json.load(image.extractfile("manifest.json"))
                self.assertEqual(manifest[0]["RepoTags"], ["panzhan:test-arm64"])
                config = json.load(image.extractfile(manifest[0]["Config"]))
                self.assertEqual(config["architecture"], "arm64")
                self.assertEqual(config["os"], "linux")
                self.assertEqual(config["config"]["Entrypoint"], ["/usr/local/bin/panzhan"])
                self.assertEqual(config["config"]["Cmd"][-2:], ["-root", "/downloads"])

                layer_name = manifest[0]["Layers"][0]
                layer_data = image.extractfile(layer_name).read()
                self.assertEqual(config["rootfs"]["diff_ids"], [
                    "sha256:" + hashlib.sha256(layer_data).hexdigest()
                ])

                with tarfile.open(fileobj=io.BytesIO(layer_data)) as layer:
                    binary_member = layer.getmember("usr/local/bin/panzhan")
                    self.assertEqual(binary_member.mode, 0o755)
                    self.assertEqual(layer.extractfile(binary_member).read(), binary.read_bytes())
                    self.assertEqual(
                        layer.extractfile("etc/ssl/certs/ca-certificates.crt").read().decode("ascii"),
                        "test-ca-bundle",
                    )
                    self.assertEqual(
                        layer.extractfile("usr/share/zoneinfo/Asia/Shanghai").read(),
                        b"test-zoneinfo",
                    )
                    license_files = {
                        "LICENSE": ROOT / "LICENSE",
                        "THIRD_PARTY_NOTICES.md": ROOT / "THIRD_PARTY_NOTICES.md",
                        "licenses/PanSou-MIT.txt": ROOT / "licenses/PanSou-MIT.txt",
                        "licenses/Go-x-net-BSD.txt": ROOT / "licenses/Go-x-net-BSD.txt",
                        "licenses/Work-Sans-OFL.txt": ROOT / "internal/web/assets/fonts/OFL.txt",
                    }
                    for name, source in license_files.items():
                        with self.subTest(license=name):
                            target = "usr/share/doc/panzhan/" + name
                            self.assertIn(target, layer.getnames(), "image must include its license texts")
                            self.assertEqual(layer.extractfile(target).read(), source.read_bytes())

    def test_rejects_non_arm64_binary(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            binary = root / "panzhan"
            ca_bundle = root / "ca-certificates.crt"
            timezone = root / "Shanghai"
            write_elf(binary, 62)
            ca_bundle.write_text("test-ca-bundle", encoding="ascii")
            timezone.write_bytes(b"test-zoneinfo")

            result = subprocess.run(
                [sys.executable, str(BUILDER), "--binary", str(binary),
                 "--ca-bundle", str(ca_bundle), "--timezone", str(timezone),
                 "--output", str(root / "invalid.tar")],
                capture_output=True,
                text=True,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("ARM64", result.stderr)


if __name__ == "__main__":
    unittest.main()
