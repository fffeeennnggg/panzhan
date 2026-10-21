"""Build a single-layer ARM64 Docker image archive without a Docker daemon."""
from __future__ import annotations

import argparse
import hashlib
import io
import json
from pathlib import Path
import tarfile


CREATED = "2026-10-19T00:00:00Z"
ROOT = Path(__file__).resolve().parents[1]


def write_json(archive: tarfile.TarFile, name: str, value):
    data = (json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
    info = tarfile.TarInfo(name)
    info.size = len(data)
    info.mode = 0o644
    info.uid = info.gid = 0
    info.uname = info.gname = ""
    info.mtime = 0
    archive.addfile(info, io.BytesIO(data))
    return data


def add_layer_file(layer: tarfile.TarFile, source: Path, target: str, mode: int):
    data = source.read_bytes()
    info = tarfile.TarInfo(target)
    info.size = len(data)
    info.mode = mode
    info.uid = info.gid = 0
    info.uname = info.gname = ""
    info.mtime = 0
    layer.addfile(info, io.BytesIO(data))


def make_image_archive(binary: Path, ca_bundle: Path, timezone_file: Path, output: Path, tag: str):
    for source in (binary, ca_bundle, timezone_file):
        if not source.is_file():
            raise ValueError(f"required image input does not exist: {source}")

    header = binary.read_bytes()[:64]
    if len(header) < 64 or header[:6] != bytes.fromhex("7f454c460201") or int.from_bytes(header[18:20], "little") != 183:
        raise ValueError("the image requires a Linux ARM64 ELF binary")

    layer_buffer = io.BytesIO()
    with tarfile.open(fileobj=layer_buffer, mode="w", format=tarfile.PAX_FORMAT) as layer:
        for directory in (
            "etc", "etc/ssl", "etc/ssl/certs", "usr", "usr/local", "usr/local/bin",
            "usr/share", "usr/share/zoneinfo", "usr/share/zoneinfo/Asia",
            "usr/share/doc", "usr/share/doc/panzhan", "usr/share/doc/panzhan/licenses",
        ):
            info = tarfile.TarInfo(directory)
            info.type = tarfile.DIRTYPE
            info.mode = 0o755
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            info.mtime = 0
            layer.addfile(info)
        add_layer_file(layer, binary, "usr/local/bin/panzhan", 0o755)
        add_layer_file(layer, ca_bundle, "etc/ssl/certs/ca-certificates.crt", 0o644)
        add_layer_file(layer, timezone_file, "usr/share/zoneinfo/Asia/Shanghai", 0o644)
        for name in ("LICENSE", "THIRD_PARTY_NOTICES.md"):
            add_layer_file(layer, ROOT / name, "usr/share/doc/panzhan/" + name, 0o644)
        for source in sorted((ROOT / "licenses").rglob("*")):
            if source.is_file():
                name = source.relative_to(ROOT / "licenses").as_posix()
                add_layer_file(layer, source, "usr/share/doc/panzhan/licenses/" + name, 0o644)
        add_layer_file(layer, ROOT / "internal/web/assets/fonts/OFL.txt",
                       "usr/share/doc/panzhan/licenses/Work-Sans-OFL.txt", 0o644)

    layer_data = layer_buffer.getvalue()
    layer_diff_id = "sha256:" + hashlib.sha256(layer_data).hexdigest()
    layer_id = hashlib.sha256((tag + "\n" + layer_diff_id).encode("utf-8")).hexdigest()

    config = {
        "architecture": "arm64",
        "os": "linux",
        "created": CREATED,
        "config": {
            "Env": ["PATH=/usr/local/bin:/usr/bin:/bin", "TZ=Asia/Shanghai"],
            "Entrypoint": ["/usr/local/bin/panzhan"],
            "Cmd": ["-listen", "0.0.0.0:9867", "-data", "/data", "-root", "/downloads"],
            "ExposedPorts": {"9867/tcp": {}},
            "WorkingDir": "/data",
        },
        "container_config": {
            "Env": ["PATH=/usr/local/bin:/usr/bin:/bin", "TZ=Asia/Shanghai"],
            "Entrypoint": ["/usr/local/bin/panzhan"],
            "Cmd": ["-listen", "0.0.0.0:9867", "-data", "/data", "-root", "/downloads"],
            "ExposedPorts": {"9867/tcp": {}},
            "WorkingDir": "/data",
        },
        "rootfs": {"type": "layers", "diff_ids": [layer_diff_id]},
        "history": [{"created": CREATED, "created_by": "PanZhan ARM64 standalone image", "empty_layer": False}],
    }
    config_bytes = (json.dumps(config, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
    config_name = hashlib.sha256(config_bytes).hexdigest() + ".json"
    layer_name = f"{layer_id}/layer.tar"
    manifest = [{"Config": config_name, "RepoTags": [tag], "Layers": [layer_name]}]
    repositories = {tag.rsplit(":", 1)[0]: {tag.rsplit(":", 1)[1]: layer_id}}

    output.parent.mkdir(parents=True, exist_ok=True)
    temporary = output.with_suffix(output.suffix + ".tmp")
    with tarfile.open(temporary, mode="w", format=tarfile.PAX_FORMAT) as archive:
        write_json(archive, "manifest.json", manifest)
        write_json(archive, "repositories", repositories)
        write_json(archive, config_name, config)

        version = b"1.0\n"
        version_info = tarfile.TarInfo(f"{layer_id}/VERSION")
        version_info.size = len(version)
        version_info.mode = 0o644
        version_info.uid = version_info.gid = 0
        version_info.uname = version_info.gname = ""
        version_info.mtime = 0
        archive.addfile(version_info, io.BytesIO(version))

        layer_metadata = {
            "id": layer_id,
            "created": CREATED,
            "container_config": {"Cmd": ["PanZhan ARM64 standalone image"]},
        }
        write_json(archive, f"{layer_id}/json", layer_metadata)

        layer_info = tarfile.TarInfo(layer_name)
        layer_info.size = len(layer_data)
        layer_info.mode = 0o644
        layer_info.uid = layer_info.gid = 0
        layer_info.uname = layer_info.gname = ""
        layer_info.mtime = 0
        archive.addfile(layer_info, io.BytesIO(layer_data))

    temporary.replace(output)
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--ca-bundle", type=Path, required=True)
    parser.add_argument("--timezone", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--tag", default="panzhan:0.2.2-arm64")
    args = parser.parse_args()
    result = make_image_archive(args.binary, args.ca_bundle, args.timezone, args.output, args.tag)
    digest = hashlib.sha256(result.read_bytes()).hexdigest()
    print(f"Docker image archive created: {result} ({result.stat().st_size} bytes, sha256:{digest})")


if __name__ == "__main__":
    main()
