#!/bin/sh
set -e

REPO="vcnkl/rpm"
BINARY="rpm"
INSTALL_DIR="${HOME}/bin"

main() {
    skip_verify="${RPM_SKIP_VERIFY:-0}"
    while [ $# -gt 0 ]; do
        case "$1" in
            --skip-verify) skip_verify=1 ;;
            --dir*)
                dir="${1#--dir}"
                dir="${dir#=}"
                if [ -z "$dir" ]; then
                    shift
                    dir="$1"
                fi
                if [ -z "$dir" ]; then
                    echo "Error: --dir requires an argument" >&2
                    exit 1
                fi
                INSTALL_DIR="$dir"
                ;;
            *)
                echo "Error: unknown argument: $1" >&2
                exit 1
                ;;
        esac
        shift
    done

    os=$(detect_os)
    arch=$(detect_arch)

    if [ "$os" = "windows" ]; then
        echo "Error: Windows is not supported by this installer." >&2
        echo "Please download the binary manually from https://github.com/${REPO}/releases" >&2
        exit 1
    fi

    version=$(get_latest_version)
    if [ -z "$version" ]; then
        echo "Error: Could not determine latest version" >&2
        exit 1
    fi

    echo "Installing ${BINARY} ${version} for ${os}/${arch}..."

    archive_name="${BINARY}_${version#v}_${os}_${arch}.tar.gz"
    download_url="https://github.com/${REPO}/releases/download/${version}/${archive_name}"

    tmp_dir=$(mktemp -d)
    trap 'rm -rf "$tmp_dir"' EXIT

    echo "Downloading ${download_url}..."
    download_file "$download_url" "${tmp_dir}/archive.tar.gz"

    if [ "$skip_verify" = "1" ]; then
        echo "Skipping checksum verification (--skip-verify)."
    else
        verify_checksum "$tmp_dir" "$version" "$archive_name"
    fi

    echo "Extracting..."
    tar -xzf "${tmp_dir}/archive.tar.gz" -C "$tmp_dir"

    mkdir -p "$INSTALL_DIR"
    mv "${tmp_dir}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    chmod +x "${INSTALL_DIR}/${BINARY}"

    echo ""
    echo "Successfully installed ${BINARY} to ${INSTALL_DIR}/${BINARY}"

    if ! echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
        echo ""
        echo "Add ${INSTALL_DIR} to your PATH:"
        echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    fi
}

download_file() {
    url="$1"
    out="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$out"
    elif command -v wget >/dev/null 2>&1; then
        wget -q "$url" -O "$out"
    else
        echo "Error: curl or wget is required" >&2
        exit 1
    fi
}

verify_checksum() {
    tmp_dir="$1"
    version="$2"
    archive_name="$3"

    echo "Verifying checksum..."
    checksums_url="https://github.com/${REPO}/releases/download/${version}/checksums.txt"
    if ! download_file "$checksums_url" "${tmp_dir}/checksums.txt"; then
        echo "Error: failed to download checksums.txt for verification" >&2
        echo "Re-run with --skip-verify to bypass (not recommended)." >&2
        exit 1
    fi

    expected=$(awk -v name="$archive_name" '$2 == name {print $1}' "${tmp_dir}/checksums.txt")
    if [ -z "$expected" ]; then
        echo "Error: no checksum listed for ${archive_name}" >&2
        exit 1
    fi

    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum "${tmp_dir}/archive.tar.gz" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        actual=$(shasum -a 256 "${tmp_dir}/archive.tar.gz" | awk '{print $1}')
    else
        echo "Error: sha256sum or shasum is required to verify the download" >&2
        echo "Re-run with --skip-verify to bypass (not recommended)." >&2
        exit 1
    fi

    if [ "$expected" != "$actual" ]; then
        echo "Error: checksum mismatch for ${archive_name}" >&2
        echo "  expected: ${expected}" >&2
        echo "  actual:   ${actual}" >&2
        exit 1
    fi

    echo "Checksum verified."
}

detect_os() {
    case "$(uname -s)" in
        Linux*)  echo "linux" ;;
        Darwin*) echo "darwin" ;;
        MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
        *)
            echo "Error: Unsupported OS: $(uname -s)" >&2
            exit 1
            ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *)
            echo "Error: Unsupported architecture: $(uname -m)" >&2
            exit 1
            ;;
    esac
}

get_latest_version() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/'
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/'
    fi
}

main "$@"
