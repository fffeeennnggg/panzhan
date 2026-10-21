# 绿联 NAS ARM64 Docker 部署

此部署包面向 ARM64 的 UGOS Pro NAS，绕过第三方 UPK 安装流程，使用绿联 Docker 应用导入本地 ARM64 镜像，然后创建 Compose 项目。

## 从源码生成镜像

仓库不附带预编译镜像。在 Linux 或 WSL 中安装 Go 1.26 或更新版本、Python 3，以及系统的 `ca-certificates` 和 `tzdata` 软件包后，在仓库根目录执行：

```sh
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o dist/panzhan-linux-arm64 ./cmd/panzhan
python3 scripts/build_ugreen_docker_image.py \
  --binary dist/panzhan-linux-arm64 \
  --ca-bundle /etc/ssl/certs/ca-certificates.crt \
  --timezone /usr/share/zoneinfo/Asia/Shanghai \
  --output dist/panzhan-0.2.2-arm64.tar \
  --tag panzhan:0.2.2-arm64
```

CA 证书和时区文件路径以 Debian/Ubuntu 为例，其他发行版请替换为本机实际路径。脚本直接生成可导入的 Docker 镜像归档，构建机无需运行 Docker，也无需准备 `rootfs/`。目录中的 `Dockerfile` 是手动准备 rootfs 时的参考模板，上述步骤使用 Python 脚本打包。

## 部署

1. 在 NAS 应用中心安装并启动 Docker。
2. 将上一步生成的 `dist/panzhan-0.2.2-arm64.tar` 上传 NAS 并导入 Docker 的镜像页面。镜像标签为 `panzhan:0.2.2-arm64`。
3. 在 NAS 共享文件夹中创建一个项目目录，将本目录的 `compose.yaml` 放进去，并创建 `data` 和 `downloads` 子目录。Compose 会把此目录下的 `data` 用作配置和任务状态目录，把 `downloads` 用作下载目录。
4. 在 Docker 的“项目 → 创建”中选择这个目录和 `compose.yaml`，然后部署项目。
5. 首次部署后，在浏览器打开 `http://NAS局域网IP:9867`，使用项目 `data/setup-token.txt` 中的一次性令牌设置管理密码。成功设置后令牌会失效。
6. 登录后在“设置”中确认下载根目录为 `/downloads` 映射的目录；Agent 分类目录应设置为该目录下已有的子目录。

升级时先停止项目，将新版本镜像 tar 导入并替换 Compose 中的镜像标签。保留项目目录中的 `data` 和 `downloads`，不要删除它们。Compose 设置了 `restart: always`，NAS 重启或容器退出后会自动启动。

## 访问和权限

端口映射为 NAS 的 `9867`。此镜像不配置 HTTPS，也不应直接暴露到公网；仅在可信局域网访问，远程使用 VPN 或自行配置 HTTPS 反向代理。

容器只获得单独挂载的 `data` 和 `downloads` 两个目录，根文件系统只读，并丢弃 Linux capabilities。不要把整个 NAS 根目录或包含其他用户数据的共享目录挂载进容器。

Docker 镜像内置 Linux ARM64 盘栈程序、CA 证书和 `Asia/Shanghai` 时区数据。它不包含用户的夸克 Cookie、Agent Token、设置、任务或下载内容。
