#!/bin/bash
set -euo pipefail

readonly APP_NAME="octopus" # 发布产物和容器内的可执行文件名。
readonly OUTPUT_DIR="build" # 所有构建、归档和容器输入的根目录。
readonly VERSION="$(git describe --tags --abbrev=0 2>/dev/null || echo 'dev')" # 当前发布版本。
readonly COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')" # 当前提交短哈希。
# 更新源与启动横幅用的仓库地址, 取自本地 origin: fork 自建时自动指向自己的仓库, 无需改代码。
# 没有 origin (例如从压缩包构建) 时回退到上游。
readonly REPO="$(git remote get-url origin 2>/dev/null | sed 's|\.git$||' || true)" # 本地 origin 指向的仓库。
readonly UPDATE_REPO="${REPO:-https://github.com/bestruirui/octopus}" # 注入用的仓库地址, 没有 origin 时回退到上游。
readonly LDFLAGS="-X 'github.com/bestruirui/${APP_NAME}/internal/conf.Version=${VERSION}' \
                  -X 'github.com/bestruirui/${APP_NAME}/internal/conf.BuildTime=$(TZ='Asia/Shanghai' date +'%F %T %z')' \
                  -X 'github.com/bestruirui/${APP_NAME}/internal/conf.Author=bestrui' \
                  -X 'github.com/bestruirui/${APP_NAME}/internal/conf.Commit=${COMMIT}' \
                  -X 'github.com/bestruirui/${APP_NAME}/internal/conf.Repo=${UPDATE_REPO}' \
                  -s -w" # 注入版本信息并缩小发布二进制。

build_standard() {
    # 标准矩阵直接使用 GOOS/GOARCH，arm 固定输出 ARMv7 指令集。
    local os go_arch
    IFS=: read -r os go_arch <<<"$1"
    local build_env=(GOOS="${os}" GOARCH="${go_arch}" CGO_ENABLED=0)
    if [ "${go_arch}" = "arm" ]; then
        build_env+=(GOARM=7)
    fi
    echo "Building ${os}/${go_arch}"
    env "${build_env[@]}" go build -trimpath -o "${OUTPUT_DIR}/bin/${APP_NAME}-${os}-${go_arch}" -ldflags="${LDFLAGS}" -tags=jsoniter .
}

readonly -a STANDARD_TARGETS=(
    "linux:amd64"
    "linux:arm64"
    "linux:arm"
    "linux:386"
    "windows:amd64"
    "darwin:arm64"
    "darwin:amd64"
) # 不依赖 cgo 的固定发布矩阵。

# 构建工具不会创建父目录，因此只保留一次直接创建。
mkdir -p "${OUTPUT_DIR}/bin" "${OUTPUT_DIR}/archives"
rm -f "${OUTPUT_DIR}"/bin/"${APP_NAME}"-* "${OUTPUT_DIR}"/archives/*.zip "${OUTPUT_DIR}/archives/SHA256SUMS"

echo "Building ${APP_NAME} ${VERSION} (${COMMIT})"
for target in "${STANDARD_TARGETS[@]}"; do
    build_standard "${target}"
done

# Docker 构建已从 release.yaml 移除, 故不再往 build/docker 下暂存各平台可执行文件。

# 每个平台只替换可执行文件名，许可证报告和发布文档保持一致。
GOFLAGS="-tags=jsoniter" go run github.com/google/go-licenses/v2@v2.0.1 report . \
    --ignore "github.com/bestruirui/${APP_NAME}" >"${OUTPUT_DIR}/THIRD_PARTY_LICENSES.csv"
cp README.md LICENSE "${OUTPUT_DIR}/THIRD_PARTY_LICENSES.csv" "${OUTPUT_DIR}/archives/"
for file in "${OUTPUT_DIR}"/bin/"${APP_NAME}"-*; do
    archive_name="$(basename "${file}").zip"
    executable_name="${APP_NAME}"
    if [[ "${file}" == *-windows-* ]]; then
        executable_name="${APP_NAME}.exe"
    fi
    cp "${file}" "${OUTPUT_DIR}/archives/${executable_name}"
    (cd "${OUTPUT_DIR}/archives" && zip -q "${archive_name}" "${executable_name}" README.md LICENSE THIRD_PARTY_LICENSES.csv)
    rm -f "${OUTPUT_DIR:?}/archives/${executable_name}"
done
rm -f "${OUTPUT_DIR:?}/archives/README.md" "${OUTPUT_DIR}/archives/LICENSE" \
    "${OUTPUT_DIR}/archives/THIRD_PARTY_LICENSES.csv"
(cd "${OUTPUT_DIR}/archives" && sha256sum ./*.zip >SHA256SUMS)
echo "Artifacts: ${OUTPUT_DIR}/archives"
